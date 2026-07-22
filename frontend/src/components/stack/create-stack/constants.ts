export const DEFAULT_COMPOSE = `# Starter template — edit the service below or replace it entirely.
services:
  app:
    image: nginx:latest          # image to run
    restart: unless-stopped      # auto-restart unless you stop it
    ports:
      - "8080:80"                # host:container
    # volumes:
    #   - ./data:/usr/share/nginx/html
    # environment:
    #   - KEY=value
`
