package commands

// This file will contain the receive-pack command implementation.
//
// receive-pack delivers site content as a Git-format packfile.
// It supports:
//   - Full site delivery (first visit)
//   - Incremental updates via --have (return visits)
//   - Subset delivery for specific routes
//
// The packfile format follows Git's pack format:
//   - 4-byte signature: "PACK"
//   - 4-byte version (network byte order): 2
//   - 4-byte number of objects (network byte order)
//   - N compressed objects (each with type + size + data)
//   - 20-byte SHA-1 checksum of all preceding content
//
// Objects types used:
//   - blob: raw file content (HTML, CSS, JS, images)
//   - tree: directory listing mapping names to blob/tree SHAs
//   - ofs-delta / ref-delta: delta-compressed objects for incremental updates
