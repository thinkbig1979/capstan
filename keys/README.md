# Git SSH Keys

Place SSH private keys here for git repository access. Mount this directory as read-only into the container at `/app/keys`.

## Setup

1. Copy your SSH private key(s) into this directory:
   ```bash
   cp ~/.ssh/id_rsa ./keys/my-project-key
   chmod 600 ./keys/my-project-key
   ```

2. In the Docker Manager UI:
   - **Global default**: Settings > Git > set SSH Key Path to `/app/keys/my-project-key`
   - **Per-stack override**: Stack detail > Git Status > Git Credentials > SSH key > set path to `/app/keys/another-key`

## Multiple Keys

You can store multiple keys for different repositories:
```
keys/
  github-deploy-key
  gitlab-deploy-key
  bitbucket-key
```

Then configure each stack to use the appropriate key path.

## Security

- This directory is mounted read-only (`:ro`) in the container
- Ensure key files have `600` permissions: `chmod 600 keys/*`
- Never commit private keys to version control (the `.gitignore` excludes `keys/*`)
