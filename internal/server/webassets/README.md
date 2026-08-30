# Embedded web UI

The built UI (index.html + assets/) is emitted here by `npx vite build`
(run inside web/) and embedded into the Go binary via //go:embed.
The generated files are git-ignored; this placeholder keeps the directory
present so a fresh `go build` has something to embed even before the
frontend is built.
