// Package server implements the multi-site sshttpd daemon.
//
// A single Server instance can host multiple SiteConfig entries, each
// listening on its own configured port with its own host key, command
// handler, and rate limiter. Connections are routed to a site by which
// listener accepted them — multi-site on a shared port (SNI-style routing
// via the SSH `User` field) is a separate concern handled in Plan B.
package server

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bugscave/sshttpd/internal/auth"
	"github.com/bugscave/sshttpd/internal/commands"
	"github.com/bugscave/sshttpd/internal/config"
	"github.com/bugscave/sshttpd/internal/ratelimit"
	"golang.org/x/crypto/ssh"
)

// Server is the main sshttpd daemon. Holds one siteListener per configured site.
type Server struct {
	cfg      *config.Config
	sites    []*siteListener
	mu       sync.Mutex
	closed   bool
}

// siteListener owns the per-site state: host key, ssh config, command handler,
// rate limiter, and TCP listener.
type siteListener struct {
	site           config.SiteConfig
	sshCfg         *ssh.ServerConfig
	handler        *commands.Handler
	authorizedKeys *auth.AuthorizedKeys
	limiter        *ratelimit.Limiter
	listener       net.Listener
}

// New creates a new server from the given configuration.
func New(cfg *config.Config) (*Server, error) {
	if len(cfg.Sites) == 0 {
		return nil, fmt.Errorf("no sites configured")
	}

	srv := &Server{cfg: cfg}
	for i := range cfg.Sites {
		sl, err := newSiteListener(cfg, i)
		if err != nil {
			return nil, fmt.Errorf("site %s: %w", cfg.Sites[i].Host, err)
		}
		srv.sites = append(srv.sites, sl)
	}
	return srv, nil
}

func newSiteListener(cfg *config.Config, idx int) (*siteListener, error) {
	site := cfg.Sites[idx]

	authorizedKeys, err := auth.LoadAuthorizedKeys(site.AuthorizedKeys)
	if err != nil {
		return nil, fmt.Errorf("loading authorized keys: %w", err)
	}

	sshCfg := &ssh.ServerConfig{
		NoClientAuth: true,
		PublicKeyCallback: func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			authorizedKeys.Reload()
			tier := auth.ClassifyKey(key, &site.Auth, authorizedKeys)
			publine := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
			if comment := authorizedKeys.Comment(key); comment != "" {
				publine += " " + comment
			}
			return &ssh.Permissions{
				Extensions: map[string]string{
					"auth-tier":       tier,
					"key-fingerprint": ssh.FingerprintSHA256(key),
					"public-key":      publine,
				},
			}, nil
		},
	}

	if site.HostKey != "" {
		if err := loadHostKey(sshCfg, site.HostKey); err != nil {
			return nil, fmt.Errorf("loading host key: %w", err)
		}
	}

	handler, err := commands.NewHandlerForSite(cfg, idx)
	if err != nil {
		return nil, fmt.Errorf("creating handler: %w", err)
	}

	limiter := ratelimit.New(site.Limits)

	return &siteListener{
		site:           site,
		sshCfg:         sshCfg,
		handler:        handler,
		authorizedKeys: authorizedKeys,
		limiter:        limiter,
	}, nil
}

// ListenAndServe starts a listener for every configured site and blocks until
// any listener fails or Close is called.
func (s *Server) ListenAndServe() error {
	errCh := make(chan error, len(s.sites))
	var wg sync.WaitGroup

	for _, sl := range s.sites {
		addr := fmt.Sprintf(":%d", sl.site.Port)
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return fmt.Errorf("listen %s: %w", addr, err)
		}
		sl.listener = ln
		log.Printf("sshttpd listening on %s for site %s", addr, sl.site.Host)

		wg.Add(1)
		go func(sl *siteListener) {
			defer wg.Done()
			if err := s.acceptLoop(sl); err != nil {
				errCh <- err
			}
		}(sl)
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) acceptLoop(sl *siteListener) error {
	for {
		conn, err := sl.listener.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed {
				return nil
			}
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			log.Printf("[%s] accept error: %v", sl.site.Host, err)
			continue
		}
		go s.handleConnection(sl, conn)
	}
}

// Close shuts down the server.
func (s *Server) Close() {
	s.mu.Lock()
	s.closed = true
	for _, sl := range s.sites {
		if sl.listener != nil {
			sl.listener.Close()
		}
	}
	s.mu.Unlock()
}

func (s *Server) handleConnection(sl *siteListener, netConn net.Conn) {
	defer netConn.Close()

	sshConn, chans, reqs, err := ssh.NewServerConn(netConn, sl.sshCfg)
	if err != nil {
		log.Printf("[%s] ssh handshake failed from %s: %v", sl.site.Host, netConn.RemoteAddr(), err)
		return
	}
	defer sshConn.Close()

	tier := auth.TierAnonymous
	fingerprint := ""
	pubkey := ""
	if sshConn.Permissions != nil {
		if t, ok := sshConn.Permissions.Extensions["auth-tier"]; ok {
			tier = t
		}
		fingerprint = sshConn.Permissions.Extensions["key-fingerprint"]
		pubkey = sshConn.Permissions.Extensions["public-key"]
	}

	log.Printf("[%s] connection from %s (user=%s, tier=%s, fp=%s)",
		sl.site.Host, netConn.RemoteAddr(), sshConn.User(), tier, fingerprint)

	go ssh.DiscardRequests(reqs)

	for newChannel := range chans {
		if newChannel.ChannelType() != "session" {
			newChannel.Reject(ssh.UnknownChannelType, "only session channels are supported")
			continue
		}

		channel, requests, err := newChannel.Accept()
		if err != nil {
			log.Printf("[%s] channel accept error: %v", sl.site.Host, err)
			continue
		}

		go s.handleSession(sl, channel, requests, tier, fingerprint, pubkey)
	}
}

func (s *Server) handleSession(sl *siteListener, channel ssh.Channel, requests <-chan *ssh.Request, tier, fingerprint, pubkey string) {
	defer channel.Close()

	sess := commands.SessionInfo{Tier: tier, Fingerprint: fingerprint, PublicKey: pubkey}
	for req := range requests {
		switch req.Type {
		case "env":
			// SSH env request: [uint32 name_len][name][uint32 value_len][value]
			if name, value, ok := parseEnvPayload(req.Payload); ok {
				if name == "SSHWEB_PUBKEY" {
					sess.PublicKey = value
				}
				req.Reply(true, nil)
			} else {
				req.Reply(false, nil)
			}

		case "exec":
			if len(req.Payload) < 4 {
				req.Reply(false, nil)
				continue
			}
			cmdLen := int(req.Payload[0])<<24 | int(req.Payload[1])<<16 |
				int(req.Payload[2])<<8 | int(req.Payload[3])
			if len(req.Payload) < 4+cmdLen {
				req.Reply(false, nil)
				continue
			}
			cmd := string(req.Payload[4 : 4+cmdLen])
			req.Reply(true, nil)

			s.executeCommand(sl, channel, cmd, sess)
			return

		case "shell":
			req.Reply(true, nil)
			s.interactiveSession(sl, channel, sess)
			return

		default:
			req.Reply(false, nil)
		}
	}
}

func (s *Server) executeCommand(sl *siteListener, channel ssh.Channel, cmd string, sess commands.SessionInfo) {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		log.Printf("[%s] (tier=%s) exec: <empty>", sl.site.Host, sess.Tier)
		fmt.Fprintf(channel.Stderr(), "error: empty command\n")
		channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{2}))
		return
	}

	log.Printf("[%s] (tier=%s) exec: %s %s", sl.site.Host, sess.Tier, parts[0], strings.Join(parts[1:], " "))

	if !sl.limiter.Allow(sess.Tier) {
		log.Printf("[%s] (tier=%s) rate-limited: %s", sl.site.Host, sess.Tier, parts[0])
		fmt.Fprintf(channel.Stderr(), "error: rate limit exceeded for tier %q\n", sess.Tier)
		channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{3}))
		return
	}

	start := time.Now()
	err := sl.handler.ExecuteBinary(parts[0], parts[1:], sess, channel)
	elapsed := time.Since(start)
	if err != nil {
		log.Printf("[%s] (tier=%s) error after %s: %v", sl.site.Host, sess.Tier, elapsed, err)
		fmt.Fprintf(channel.Stderr(), "error: %v\n", err)
		channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{1}))
		return
	}
	log.Printf("[%s] (tier=%s) ok in %s: %s", sl.site.Host, sess.Tier, elapsed, parts[0])
	channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
}

func parseEnvPayload(payload []byte) (name, value string, ok bool) {
	if len(payload) < 4 {
		return "", "", false
	}
	nameLen := int(payload[0])<<24 | int(payload[1])<<16 | int(payload[2])<<8 | int(payload[3])
	if len(payload) < 4+nameLen+4 {
		return "", "", false
	}
	name = string(payload[4 : 4+nameLen])
	off := 4 + nameLen
	valLen := int(payload[off])<<24 | int(payload[off+1])<<16 | int(payload[off+2])<<8 | int(payload[off+3])
	off += 4
	if len(payload) < off+valLen {
		return "", "", false
	}
	value = string(payload[off : off+valLen])
	return name, value, true
}

func (s *Server) interactiveSession(sl *siteListener, channel ssh.Channel, sess commands.SessionInfo) {
	fmt.Fprintf(channel, "ssh-web/0.1 ready (tier: %s)\n> ", sess.Tier)

	buf := make([]byte, 4096)
	for {
		n, err := channel.Read(buf)
		if err != nil {
			return
		}
		line := strings.TrimSpace(string(buf[:n]))
		if line == "" {
			fmt.Fprintf(channel, "> ")
			continue
		}
		if line == "quit" || line == "exit" {
			return
		}

		parts := strings.Fields(line)
		log.Printf("[%s] (tier=%s) shell: %s %s", sl.site.Host, sess.Tier, parts[0], strings.Join(parts[1:], " "))

		if !sl.limiter.Allow(sess.Tier) {
			log.Printf("[%s] (tier=%s) rate-limited: %s", sl.site.Host, sess.Tier, parts[0])
			fmt.Fprintf(channel, "error: rate limit exceeded for tier %q\n> ", sess.Tier)
			continue
		}

		start := time.Now()
		resp, err := sl.handler.Execute(parts[0], parts[1:], sess)
		elapsed := time.Since(start)
		if err != nil {
			log.Printf("[%s] (tier=%s) error after %s: %v", sl.site.Host, sess.Tier, elapsed, err)
			fmt.Fprintf(channel, "error: %v\n> ", err)
			continue
		}
		log.Printf("[%s] (tier=%s) ok in %s: %s", sl.site.Host, sess.Tier, elapsed, parts[0])
		fmt.Fprintf(channel, "%s\n> ", resp)
	}
}

func loadHostKey(cfg *ssh.ServerConfig, path string) error {
	keyBytes, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		log.Printf("host key not found at %s, generating new ed25519 key...", path)
		return generateAndLoadHostKey(cfg, path)
	}

	signer, err := ssh.ParsePrivateKey(keyBytes)
	if err != nil {
		return fmt.Errorf("parsing host key: %w", err)
	}

	cfg.AddHostKey(signer)
	return nil
}

func generateAndLoadHostKey(cfg *ssh.ServerConfig, path string) error {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("generating key: %w", err)
	}

	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		return fmt.Errorf("creating signer: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("creating key directory: %w", err)
	}

	privBytes, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		return fmt.Errorf("marshaling private key: %w", err)
	}
	pemData := pem.EncodeToMemory(privBytes)
	if err := os.WriteFile(path, pemData, 0600); err != nil {
		return fmt.Errorf("writing host key: %w", err)
	}

	log.Printf("host key saved to %s", path)
	log.Printf("fingerprint: %s", ssh.FingerprintSHA256(signer.PublicKey()))

	cfg.AddHostKey(signer)
	return nil
}
