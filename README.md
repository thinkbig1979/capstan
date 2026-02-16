# Docker Manager

A web-based Docker Compose stack manager with Git integration.

## Quick Start

```bash
# Start everything (backend + frontend)
./start-local.sh
```

Then open http://localhost:3001

## Features

- **Docker Compose Management**: Create, start, stop, restart, and delete stacks
- **Compose Editor**: Edit docker-compose.yaml files with live linting
- **Environment Files**: Manage .env files with comment preservation
- **Git Integration**: Status, pull, log, and diff for git-managed stacks
- **Real-time Updates**: File watching for automatic stack detection
- **Action Logging**: Audit trail of all operations

## Volume Path Identity

**Important:** Docker Manager requires that the `STACKS_DIR` path inside the container must match the path on the host system for Docker Compose operations to work correctly.

### Quick Setup

Add both environment variables to your `docker-compose.yaml`:

```yaml
environment:
  - STACKS_DIR=/opt/stacks
  - HOST_STACKS_DIR=/opt/stacks
```

### Verification

On startup, Docker Manager validates path identity and logs warnings if paths don't match:

```bash
docker-compose logs backend | grep "Volume path identity"
```

### Detailed Documentation

See [Volume Path Identity](Supporting-Docs/Security/Volume-Path-Identity.md) for:
- Why this requirement exists
- Correct vs incorrect examples
- Troubleshooting steps
- Migration guide from Dockge

## Project Structure

```
docker-manager/
├── backend/          # Go backend API
│   ├── cmd/         # Main application
│   ├── internal/    # Internal packages
│   └── services/    # Service layer
├── frontend/        # React frontend
│   └── src/        # Source code
└── .agent-os/       # Agent OS configuration
```

## Documentation

### Core Documentation
- **[TESTING.md](TESTING.md)** - Local testing and development guide
- **[CLAUDE.md](CLAUDE.md)** - Agent OS framework instructions

### Deployment & Operations
- **[Deployment Guide](Supporting-Docs/Deployment.md)** - Production deployment, SSL/TLS configuration, environment variables, reverse proxy setup
- **[Migration from Dockge](Supporting-Docs/Migration-From-Dockge.md)** - Step-by-step migration guide from Dockge
- **[Troubleshooting Guide](Supporting-Docs/Troubleshooting.md)** - Common issues and solutions

### Security & Configuration
- **[Volume Path Identity](Supporting-Docs/Security/Volume-Path-Identity.md)** - Critical configuration requirement for Docker Compose operations

## Quick Commands

### Docker Compose (Full Stack)
```bash
./start-local.sh          # Start all services
docker-compose logs -f     # View logs
docker-compose down        # Stop all services
```

### Backend Only
```bash
cd backend
./run-local.sh           # Quick start
make run                # Make target
make test               # Run tests
```

### Frontend Only
```bash
cd frontend
./run-dev.sh            # Quick start (dev server)
npm run build           # Build for production
```

## API Endpoints

### Health
- `GET /health` - Health check

### Authentication (if enabled)
- `POST /api/v1/auth/login` - Login
- `POST /api/v1/auth/logout` - Logout

### Stacks
- `GET /api/v1/stacks` - List all stacks
- `GET /api/v1/stacks/:id` - Get stack details
- `POST /api/v1/stacks` - Create new stack
- `POST /api/v1/stacks/:id/start` - Start stack
- `POST /api/v1/stacks/:id/stop` - Stop stack
- `POST /api/v1/stacks/:id/restart` - Restart stack
- `DELETE /api/v1/stacks/:id` - Delete stack

### Compose Files
- `GET /api/v1/stacks/:id/compose` - Get compose file
- `PUT /api/v1/stacks/:id/compose` - Save compose file
- `POST /api/v1/stacks/:id/compose/lint` - Lint compose file

### Environment Files
- `GET /api/v1/stacks/:id/env` - Get env file
- `PUT /api/v1/stacks/:id/env` - Save env file

### Git
- `GET /api/v1/directories/:path/git` - Get git status
- `POST /api/v1/directories/:path/git/pull` - Pull changes
- `GET /api/v1/directories/:path/git/log` - Get commit log
- `GET /api/v1/directories/:path/git/diff/:hash` - Get commit diff

## Migration from Dockge

Migrating from Dockge? See the comprehensive [Migration from Dockge guide](Supporting-Docs/Migration-From-Dockge.md) for:
- Prerequisites and backup procedures
- Side-by-side setup (both apps running)
- Port differences (Dockge 5001 → Docker Manager 5001)
- Account migration (manual: create new admin)
- Complete feature comparison table
- Troubleshooting common migration issues

### Quick Migration

For a quick overview:

1. **Backup existing stacks:**
   ```bash
   cp -r /opt/stacks /opt/stacks.backup
   ```

2. **Update environment variables** (Dockge uses `DOCKGE_STACKS_DIR`, Docker Manager uses `STACKS_DIR`):
   ```yaml
   environment:
     - STACKS_DIR=/opt/stacks
     - HOST_STACKS_DIR=/opt/stacks
   ```

3. **Restart** service:
   ```bash
   docker-compose down
   docker-compose up -d
   ```

4. **Verify path validation:**
   ```bash
   docker-compose logs backend | grep "Volume path identity"
   ```

5. **Test with a simple stack** before migrating production data

For detailed migration steps, see the [Migration from Dockge guide](Supporting-Docs/Migration-From-Dockge.md).

## Production Deployment

For production deployment, see the comprehensive [Deployment Guide](Supporting-Docs/Deployment.md) which covers:

- **Quick Start**: Basic deployment steps
- **Production Checklist**: Security, SSL, monitoring, backups
- **Environment Variables**: Complete list with descriptions
- **Reverse Proxy**: nginx, Traefik, Caddy examples
- **SSL/TLS**: Certbot examples
- **Docker Socket Security**: Permissions and best practices

### Production Configuration

```bash
# Generate secure JWT secret
JWT_SECRET=$(openssl rand -hex 32)

# Create production .env file
cat > .env << EOF
PORT=5001
LOG_LEVEL=info
JWT_SECRET=$JWT_SECRET
AUTH_DISABLED=false
STACKS_DIR=/opt/stacks
HOST_STACKS_DIR=/opt/stacks
DATA_DIR=/app/data
TRUSTED_NETWORKS=172.16.0.0/12,10.0.0.0/8,192.168.0.0/16,127.0.0.1
EOF
```

### Security Considerations

- **Always set a strong JWT secret** (min 32 characters)
- **Enable authentication** in production (`AUTH_DISABLED=false`)
- **Use SSL/TLS** for all connections
- **Mount Docker socket as read-only** (`/var/run/docker.sock:/var/run/docker.sock:ro`)
- **Configure trusted networks** for access control
- **Set up regular backups** of stack configurations
- **Monitor resource usage** and set appropriate limits
- **Use a reverse proxy** (nginx, Traefik, Caddy) with SSL termination

For detailed production deployment instructions, see the [Deployment Guide](Supporting-Docs/Deployment.md).

## Development

### Backend
- Language: Go 1.24
- Database: SQLite
- Framework: Gin
- Docker SDK: go-docker
- Git Library: go-git

### Frontend
- Language: TypeScript
- Framework: React + Vite
- UI: Tailwind CSS
- State: TanStack Query
- Editor: CodeMirror 6

## License

MIT
