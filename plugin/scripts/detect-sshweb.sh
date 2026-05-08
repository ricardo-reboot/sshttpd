#!/bin/bash
# Detect SSH-Web URLs or domains in user prompts and hint Claude to probe them.
#
# Runs on every UserPromptSubmit. Extracts domains/URLs from the prompt,
# checks if port 22443 is open (fast TCP probe), and if so, injects a
# system reminder telling Claude to connect via sshweb_connect.

PROMPT="$1"

# Extract potential domains/URLs from the prompt
DOMAINS=$(echo "$PROMPT" | grep -oE '(ssh-web://)?[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?(\.[a-zA-Z]{2,})+' | sed 's|ssh-web://||' | sort -u)

if [ -z "$DOMAINS" ]; then
    exit 0
fi

FOUND=""
for domain in $DOMAINS; do
    # Skip common non-SSH-Web domains
    case "$domain" in
        *.google.com|*.github.com|*.githubusercontent.com|*.npmjs.org|*.pypi.org|*.stackoverflow.com)
            continue
            ;;
    esac

    # Fast TCP probe — 1 second timeout
    if nc -z -w1 "$domain" 22443 2>/dev/null; then
        FOUND="$FOUND $domain"
    fi
done

if [ -n "$FOUND" ]; then
    echo "<system-reminder>"
    echo "SSH-Web detected: the following domains have port 22443 open:$FOUND"
    echo "Use the sshweb_connect tool to discover available MCP tools from these sites."
    echo "</system-reminder>"
fi
