package api

// BuildVersion is the version string reported by GET /health and written to
// the startup log. This fork keeps the upstream module layout but versions
// itself independently so clients (and issue reports) can tell builds apart.
const BuildVersion = "3.4.1-hardened"
