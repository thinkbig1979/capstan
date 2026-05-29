# Capstan - Local Testing Guide

## Quick Start

### Option 1: Docker Compose (Recommended)

Start everything with a single command:

```bash
./start-local.sh
```

This will:
- Build and start backend on `http://localhost:5001`
- Build and start frontend on `http://localhost:3001`
- Create necessary directories
- Set up environment variables

**Access the application:** http://localhost:3001

### Option 2: Backend Only (Native Go)

Start just the backend (for development):

```bash
cd backend
./run-local.sh
```

**Access the API:** http://localhost:5001

### Option 3: Frontend Only (Native Node)

Start just the frontend (requires backend running):

```bash
cd frontend
./run-dev.sh
```

**Access the UI:** http://localhost:5173

## Directory Structure

```
capstan/
├── backend/              # Go backend
│   ├── cmd/server/      # Main application
│   ├── internal/        # Internal packages
│   ├── .env            # Environment variables (created by start script)
│   ├── Dockerfile       # Backend Docker image
│   └── run-local.sh    # Quick start script
├── frontend/            # React frontend
│   ├── src/           # Source code
│   ├── dist/          # Built assets
│   ├── .env           # Environment variables
│   ├── Dockerfile     # Frontend Docker image
│   └── run-dev.sh     # Dev server script
├── docker-compose.yaml  # Multi-service compose file
└── start-local.sh      # Quick start script
```

## Configuration

### Backend Environment Variables

See `backend/.env.example` for all available options:

| Variable | Description | Default |
|----------|-------------|---------|
| `JWT_SECRET` | JWT signing key (min 32 chars) | Required |
| `PORT` | Server port | `5001` |
| `STACKS_DIR` | Directory for Docker Compose files | `/opt/stacks` |
| `DATA_DIR` | Directory for database/logs | `/app/data` |
| `AUTH_DISABLED` | Disable authentication | `true` (local) |
| `GIT_SSH_KEY` | Path to SSH key for git | `~/.ssh/id_rsa` |

### Frontend Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `VITE_API_BASE_URL` | Backend API URL | `http://localhost:5001` |

## Docker Compose Commands

```bash
# Start all services
docker-compose up -d

# Start with rebuild
docker-compose up -d --build

# View logs
docker-compose logs -f

# View specific service logs
docker-compose logs -f backend
docker-compose logs -f frontend

# Stop services
docker-compose down

# Stop and remove volumes (WARNING: deletes data)
docker-compose down -v
```

## Manual Docker Build

### Backend

```bash
cd backend
docker build -t capstan-backend .
docker run -d \
  -p 5001:5001 \
  -v /var/run/docker.sock:/var/run/docker.sock:ro \
  -v $(pwd)/stacks:/opt/stacks \
  -v $(pwd)/data:/app/data \
  --env-file .env \
  --name capstan-backend \
  capstan-backend
```

### Frontend

```bash
cd frontend
docker build -t capstan-frontend .
docker run -d \
  -p 3001:80 \
  --name capstan-frontend \
  capstan-frontend
```

## Testing the Backend

```bash
# Health check
curl http://localhost:5001/health

# Create a test stack
curl -X POST http://localhost:5001/api/v1/stacks \
  -H "Content-Type: application/json" \
  -d '{
    "name": "nginx-test",
    "composeContent": "services:\n  web:\n    image: nginx:1.25\n    restart: always\n    ports:\n      - \"8080:80\"",
    "deploy": true
  }'

# List stacks
curl http://localhost:5001/api/v1/stacks

# Get stack details
curl http://localhost:5001/api/v1/stacks/nginx-test:default
```

## Stacks Directory

When running locally, Docker Compose stacks are stored at:
- **Docker Compose**: Volume `stacks_data` mounted to `/opt/stacks`
- **Local Go**: `/tmp/stacks`

You can add your own Docker Compose files here and they'll be detected automatically.

## Troubleshooting

### Backend won't start

1. Check Docker is running: `docker info`
2. Check logs: `docker-compose logs backend`
3. Verify port 5001 isn't in use: `lsof -i :5001`

### Frontend won't connect to backend

1. Verify backend is running: `curl http://localhost:5001/health`
2. Check `VITE_API_BASE_URL` in frontend/.env
3. Check CORS origins in backend/.env

### Docker socket permission denied

The backend needs access to the Docker socket. Ensure:
- Docker socket is mounted: `-v /var/run/docker.sock:/var/run/docker.sock:ro`
- User has permission to access Docker socket

### Database errors

The SQLite database is created automatically. If you see errors:
1. Check DATA_DIR is writable
2. Remove the database: `rm /tmp/capstan-data/capstan.db` (or docker volume)
3. Restart the backend

## Development

### Backend Development

```bash
cd backend
make run          # Run with auto-reload (if using air)
make test          # Run tests
make build         # Build binary
```

### Frontend Development

```bash
cd frontend
npm run dev        # Start dev server
npm run build      # Build for production
npm run test       # Run tests
```

## Production Deployment

1. Change `AUTH_DISABLED=false` in backend/.env
2. Set a strong `JWT_SECRET`
3. Update `CORS_ORIGINS` to your domain
4. Build and deploy using Docker Compose or individual containers
5. Use reverse proxy (nginx, traefik) for SSL

## Security Notes

- For local testing, authentication is disabled (`AUTH_DISABLED=true`)
- In production, always enable authentication and use strong JWT secrets
- The Docker socket is mounted read-only to minimize risk
- Consider using Docker secrets for sensitive data in production
