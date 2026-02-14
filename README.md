# Docker Manager

A web-based Docker Compose stack manager with Git integration.

## Quick Start

```bash
# Start everything (backend + frontend)
./start-local.sh
```

Then open http://localhost:3000

## Features

- **Docker Compose Management**: Create, start, stop, restart, and delete stacks
- **Compose Editor**: Edit docker-compose.yaml files with live linting
- **Environment Files**: Manage .env files with comment preservation
- **Git Integration**: Status, pull, log, and diff for git-managed stacks
- **Real-time Updates**: File watching for automatic stack detection
- **Action Logging**: Audit trail of all operations

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

- **[TESTING.md](TESTING.md)** - Local testing and development guide
- **[CLAUDE.md](CLAUDE.md)** - Agent OS framework instructions

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
