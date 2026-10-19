# Settings help

Settings holds everything that is configured once and then left alone: your account,
where stacks live, git credentials, update scans, backups and housekeeping. The
sidebar groups the sections as Account, Stacks, Automation and System, and has a
search box. Most forms save with a button, and the Updates section saves each change
as you make it.

## Account security

Your username, when the account was created, and the password form.

**Common tasks**

- **Change your password:** enter the current password, then the new one twice. It
  must be at least 8 characters.

**Gotchas**

- **Changing the password signs you out everywhere.** Every device has to log in
  again.
- **With authentication disabled this section is unavailable.** Capstan says so
  instead of showing the form.
- **There is no password reset by email.** A forgotten password is reset from a shell
  on the host. See [Recovering admin access](../how-to/recover-admin-access.md).

## Appearance

The theme (Light, Dark or System) and a reset for layout preferences.

**Common tasks**

- **Switch theme:** pick one. System follows your operating system.
- **Reset layout preferences:** use the button to restore the default sidebar width,
  sidebar filters, dashboard sort and filter, and section expansion.

**Gotchas**

- **Reset reloads the page.** Your theme, account and stack data are not affected.

## Directories

The folders Capstan scans for compose projects, and where new stacks go. The list
shows each monitored directory and marks the default.

**Common tasks**

- **See what is monitored:** read the list, and use the filter if it is long.
- **Add another directory:** set the `EXTRA_STACKS_DIRS` environment variable
  (comma-separated paths) and restart Capstan. This page cannot add roots.
- **Tune scanning and creation:** see the two settings below.

**Gotchas**

- **Paths are as the container sees them.** They must also match on the host. See
  [Paths, volumes and container user](../reference/configuration.md#paths-volumes--container-user).
- **Two settings need a follow-up step,** described below.

### Directory recursion depth

How many folder levels Capstan descends below each monitored directory when looking
for compose files. A depth of 1 only checks immediate subfolders.

**Common tasks**

- **Find nested stacks:** raise the depth, save, then rescan from the dashboard.

**Gotchas**

- **A change needs a rescan.** Saving alone does not add stacks to the list.

### Default directory for new stacks

The monitored directory that the **New Stack** dialog preselects.

**Common tasks**

- **Change the default:** pick one of the monitored directories and save.

**Gotchas**

- **It applies after a server restart.** Until then the form shows the directory in
  use now and the one the next start will use.
- **You can only choose an existing root.** The setting does not add or remove roots.
  See [Default stacks directory](../reference/configuration.md#default-stacks-directory).

## Global environment variables

Variables applied to every stack. Docker Compose loads them before a stack's own
`.env` file, which can still override them.

**Common tasks**

- **Add a variable:** use **Add Variable**, fill in the key and value, then **Save**.
- **Find one:** use the filter box (key or value).
- **Remove one:** use the remove icon on its row, then Save.

**Gotchas**

- **Changes apply on the next stack restart.** Running stacks keep their old values.
- **Secrets are masked.** Keys that look like passwords, tokens or API keys stay
  hidden. Reveal them by entering your password, which unlocks values for 5 minutes.
- **A stack's own `.env` wins** when both define the same key.

## Git

Default credentials for stacks that are git repositories, so Capstan can clone, pull
and read history. A credential saved on an individual stack overrides these.

**Common tasks**

- **Set credentials:** fill in the SSH key path, the HTTPS username and token, then
  save.

**Gotchas**

- **Saved values beat environment variables.** For the variables behind these fields,
  see [Git integration](../reference/configuration.md#git-integration).

### SSH private key path

The location of the private key used for git over SSH, by every folder whose git
auth is not set to its own SSH key or HTTPS.

**Common tasks**

- **Use a key:** mount the key into the container, then enter its path as the
  container sees it.

**Gotchas**

- **It is a path inside the container, not on the host.** `/home/you/.ssh/id_rsa`
  works only if that exact path exists in the container.
- **The key must be readable by the user the container runs as, and by no one
  else.** Use `chmod 600` and the container's PUID as owner. A key other users can
  read is refused, and the pull or push says so.
- **The default path counts as unset when nothing is there.** With no file at
  `$HOME/.ssh/id_rsa`, git over SSH runs as it did before Capstan applied keys, and
  push over SSH is refused. Any other path that is missing is refused.
- **Capstan trusts a server's host key the first time it connects** and keeps it in
  `known_hosts` in its data directory. If the server's host key later changes, git
  over SSH to it is refused.

### Personal access token

The token for git over HTTPS, paired with the username above (default `git`).

**Common tasks**

- **Replace the token:** type the new one and save.
- **Keep the current token:** leave the field blank.
- **Check what you typed:** use the eye icon to reveal it.

**Gotchas**

- **Give it only the access it needs.** Read access to the repositories you clone is
  enough for pulling.
- **The token is stored encrypted.** If the stored token cannot be read, for example
  after the `STORAGE_KEY` changed, the form says so and asks you to enter it again.

### Allow commit and push

Lets Capstan commit the files you choose in a stack's folder and push the branch to
its upstream. It is off by default, so Capstan cannot commit or push from a folder
until you turn it on.

**Common tasks**

- **Turn it on for a folder:** open the stack's git chip, go to Git Credentials, and
  switch on "Allow commit and push".
- **Commit and push:** save a file, then pick "Commit and push" in the save message or
  in the git panel, tick the files, write a message and confirm.

**Gotchas**

- **It is per folder.** Each stack folder has its own switch.
- **HTTPS remotes with a token, and SSH remotes with a key, are the supported
  routes.** SSH uses the folder's SSH key, or the global one in Settings > Git when
  the folder inherits. HTTP, `file://` and local-path remotes also work. `git://` and
  other transports are refused.
- **Capstan never force-pushes or sets an upstream.** If the remote has newer commits,
  pull first.

### Commit author

The name and email on commits Capstan makes. Set them in Settings > Git.

**Common tasks**

- **Set the author:** fill in both fields and save.

**Gotchas**

- **Commits are blocked until both are set.**
- **A saved value cannot be cleared from here.** Replace it with another one.

## Updates

How Capstan finds newer images, and whether it applies them for you.

**Common tasks**

- **Choose how often to scan:** see the scan interval below.
- **Check right now:** use **Check for Updates** on the dashboard Updates view.

**Gotchas**

- **Scanning only looks.** Nothing updates unless you trigger it or turn on
  auto-update.
- **The last scan result and any error are shown here.**

### Scan interval and auto-update

**Scan Interval** controls how often images are compared with their registries.
**Enable Auto-Update** is the master switch for applying what a scan finds, and it only
affects containers and stacks you opted in.

**Common tasks**

- **Scan on a schedule:** pick Every hour, 6 hours, 12 hours or 24 hours, or Custom.
- **Turn auto-update on:** switch it on, then opt in individual stacks and containers
  on their own pages.
- **Apply at a fixed time:** under Schedule choose a time of day and the days of the
  week instead of applying as soon as an update is found.

**Gotchas**

- **Custom intervals start at 15 minutes.**
- **Scans and applying are separate.** A scheduled apply needs a scan interval, because
  nothing else detects new images. With scanning disabled the form warns you.
- **Applying recreates containers.** Expect a brief interruption.
- **Times use the server's clock.** The zone is shown under the fields and comes from
  the `TZ` setting, see [Paths, volumes and container user](../reference/configuration.md#paths-volumes--container-user).
- **Failures are shown here.** A run that applied nothing, or a schedule that could not
  be armed, appears in red under the switch.

## Backup

Everything about backups in one place: the status card, the repository, retention, the
schedule and cloud sync. The guide is
[Configuring backups](../how-to/configure-backups.md).

**Common tasks**

- **Back up now:** use the button on the status card.
- **Verify backups can be restored:** use **Check backups** on the status card. It reads
  5% of the backup data and cannot run while another backup operation is working.
- **Change settings:** edit any field, then use **Save** in the bar at the bottom.
  **Discard** returns to the last saved values.
- **Stop a stack from being backed up:** switch off its Backup toggle on the stack page.

**Gotchas**

- **Edits apply only after you Save.** Fields show the last saved value until then.
- **Saved settings beat environment variables.** See
  [Backups and restic](../reference/configuration.md#backups--restic).
- **A banner explains a missing backup engine.** Backups need restic, and cloud sync
  needs rclone.
- **A killed restic run can leave a lock.** Capstan clears stale ones itself, see
  [Stale repository locks](../how-to/configure-backups.md#stale-repository-locks).

### Repository path

Where restic keeps its backups. It can be a local folder, or a restic URI for remote
storage.

**Common tasks**

- **Use a local folder:** enter a path inside the container, such as the default under
  the data directory.
- **Initialize it once:** after the password is set, use **Initialize repository**. The
  button is only active for an empty location.
- **Fall back to the environment:** clear the field to use `RESTIC_REPOSITORY`.

**Gotchas**

- **The path is inside the container.** It must be on a host bind mount to survive
  recreation. See [Bind-mount requirement](../how-to/configure-backups.md#bind-mount-requirement).
- **A credential in a URI is masked as `***`.** To change any part of it, re-enter the
  whole URI. Saving with `***` still present is rejected, which keeps the stored
  credential intact.
- **A badge shows where the value comes from.** It says whether the value is from the
  environment or was saved here. A saved value wins.
- **Initialize is refused when a repository may already exist.** That includes an
  unreachable location or a wrong password. This is deliberate: initializing would start
  a second, empty repository next to your real one.

### Repository password

The password restic uses to encrypt the repository.

**Common tasks**

- **Set it:** type it and save. Do this before initializing.
- **Keep the current one:** leave the field blank.
- **Remove a saved password:** use **Clear saved password**. It appears only when the
  password was saved here rather than set in the environment.

**Gotchas**

- **A lost password makes the repository unreadable.** There is no recovery. Store it
  in a password manager, together with `STORAGE_KEY` and `JWT_SECRET`.
- **It must match the password the repository was created with.** Entering a different
  one makes the repository unreadable to Capstan.
- **It can come from `RESTIC_PASSWORD`.** The source badge tells you which.

### Retention and pruning

How many snapshots restic keeps for each stack: daily, weekly, monthly and yearly.
After every backup, restic applies this policy to that stack and forgets the snapshots
that fall outside it. **Auto-prune after backup** adds the step that frees the disk
space those forgotten snapshots used.

**Common tasks**

- **Keep more history:** raise the counts for the periods you care about.
- **Reclaim space:** turn on auto-prune, or prune by hand.

**Gotchas**

- **0 switches that period off.** It does not keep zero snapshots. If all four are 0,
  no policy applies and nothing is forgotten.
- **Forgotten snapshots cannot be restored.** Check the counts before you lower them.
- **Without auto-prune the disk space stays used.** The data of forgotten snapshots is
  only deleted when a prune runs.

### Weekly repository check

The **Check the repository weekly** switch. After a scheduled backup, once a week,
Capstan reads 5% of the backup data to confirm it can be restored.

**Common tasks**

- **Turn it on or off:** use the switch, then Save.
- **Run a check on demand:** use Check backups on the status card.

**Gotchas**

- **It only runs with scheduled backups on.**
- **A failed check shows a warning but backups continue.**
- **It can cause a skip.** A scheduled backup that falls due while the check runs is
  skipped and shows as Skipped in the history. See
  [Checking that backups can be restored](../how-to/configure-backups.md#checking-that-backups-can-be-restored).

### Cloud sync

Copies the local restic repository to off-site storage through rclone, such as S3 or
Backblaze. **Remote** is the name you gave that storage in your rclone configuration,
and **Path on remote** is the folder inside it.

**Common tasks**

- **Connect storage:** configure the remote in rclone first, then enter its name and a
  path here and save.
- **Test it:** use **Test connectivity** before you rely on it.
- **Sync on demand:** use **Sync now**.
- **Sync automatically:** turn on **Sync to cloud after each backup** in the Backup
  schedule block.
- **Pull the repository back from the remote:** use **Restore repository from
  remote** when the local repository is lost or damaged. You type the remote name to
  confirm. Local-only files go to a `.pre-dr-<time>` folder next to the repository,
  and it does not restore the database or stacks. See
  [Restoring a backup](../how-to/restore-a-backup.md#disaster-recovery).

**Gotchas**

- **Sync uses saved settings.** Save before you press Sync now, or the button stays
  disabled.
- **The rclone config must be reachable from the container.** See
  [Cloud sync](../how-to/configure-backups.md#cloud-sync-optional).
- **A large delete asks for confirmation.** If a sync would remove many files from the
  remote, you are asked first. That is expected right after a prune. If you did not
  prune, cancel and check the local repository.

### Parallel transfers

How many files rclone copies at once during a sync, from 1 to 32 (default 4).

**Common tasks**

- **Speed up a sync:** raise it on a fast connection.
- **Be gentler on the network or provider:** lower it.

**Gotchas**

- **More is not always faster.** Providers rate-limit, and busy links slow other
  traffic.
- **It only affects sync,** not local backups.

## Docker cleanup

Docker cleanup moved to the Docker host page, beside the numbers it acts on. This
section links there. See [Docker host help](docker-host.md#automatic-cleanup).

## Audit log

A record of security-relevant actions: who did what, and when. It covers sign-ins,
account changes, stack and resource changes and prune operations. Secrets such as
passwords, tokens and env values are never written to it.

**Common tasks**

- **Find an event:** search the detail text or filter by action.
- **Control how long history is kept:** use the **History retention** form under the log.

**Gotchas**

- **History retention covers more than the audit log.** It also sets how long update,
  backup and cleanup history is kept, and a daily pass deletes anything older.
- **It never touches restic snapshots.** Only Capstan's own records are pruned.
- **Deleted history cannot be recovered.** Each field has a minimum number of days.

## About

The version, commit and build date of the running instance, plus a link to the release
notes for released builds.

**Common tasks**

- **Check what you run:** read the version before reporting a problem or upgrading. See
  [Upgrading and rolling back](../how-to/upgrade-and-roll-back.md).

**Gotchas**

- **A local build shows "dev".** It has no release notes link.

---

[← Documentation index](../../README.md#documentation)
