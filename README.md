# Auth Server

Self-hosted multi-tenant auth & subscription server built on Casdoor.
See `docs/superpowers/specs/2026-05-20-auth-server-design.md` for the design.

- `docker-compose up -d` to run Casdoor + PostgreSQL
- `verifier/` — offline JWT + subscription verification library
- `cmd/subsync/` — mirrors subscription state into user properties
