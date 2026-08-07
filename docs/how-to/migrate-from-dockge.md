# Migration from Dockge

Capstan reads the same on-disk stack layout as Dockge, so migration is mostly a
matter of pointing it at your existing stacks directory.

1. **Back up existing stacks:**
   ```bash
   cp -r /opt/stacks /opt/stacks.backup
   ```

2. **Map the stacks directory** (Dockge uses `DOCKGE_STACKS_DIR`; Capstan uses
   `STACKS_DIR`, and requires `HOST_STACKS_DIR` to match — see
   [Volume Path Identity](../explanation/security-model.md#volume-path-identity)):
   ```yaml
   environment:
     - STACKS_DIR=/opt/stacks
     - HOST_STACKS_DIR=/opt/stacks
   ```

3. **Start Capstan** and create an admin account on first run (`/auth/setup`).
   Accounts are not migrated from Dockge.

4. **Verify path validation:**
   ```bash
   docker compose logs | grep "Volume path identity"
   ```

5. **Test with a single stack** before relying on it for production.

You can run Dockge and Capstan side by side during migration as long as only one
manages a given stack at a time.

---

[← Documentation index](../../README.md#documentation)
