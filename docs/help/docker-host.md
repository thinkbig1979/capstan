# Docker host help

The Docker host page shows what Docker keeps on disk on the server Capstan runs
on: how much is in use, how much could be removed, and a list for each kind of
resource. Open it from the **Host** strip under the stack list on the dashboard.
The numbers refresh every minute and when Docker reports a change.

## Overview

The top of the page is a disk bar with Docker's total disk use, split into space in
use and space that could be reclaimed. Below it, one card per kind of resource shows
a count or a size and a line of detail. A card also shows what could be removed, with
**Clean up**, or "Nothing to clean up". **View all** opens that resource's list.
**Free up** at the top of the page shows how much one click would free.

**Common tasks**

- **Find what is using space:** compare the cards, then open the biggest with **View all**.
- **Switch between lists:** use the Containers, Images, Volumes, Networks and
  Build cache links at the top of any list.

**Gotchas**

- **Volume sizes can read "size unknown".** Docker could not measure every volume, so
  the totals count only the volumes it could. Unknown is not zero.
- **A card removes nothing by itself.** **Clean up** and **Free up** open the
  Free up space dialog, where you pick what to remove. Each list also has **Prune**
  and the row icons.
- **If a refresh fails,** the page keeps the last numbers and says the refresh failed.
  If the first load fails, it shows no numbers and offers **Retry**.

## Free up space

The **Free up space** dialog opens from **Free up** at the top of the page or from
any card's **Clean up**. It lists what Docker keeps that nothing uses, in five rows:
unused images, build cache, stopped containers outside any stack, unattached volumes
and unused networks. Tick the rows you want gone, then press **Remove selected**.

**Common tasks**

- **See exactly what goes:** open **Show the items** under a row. Long lists show the
  first 200 and how many more there are.
- **Check the total first:** the figure next to the buttons updates as you tick and
  untick rows.
- **Make it automatic:** tick **Do this automatically** before pressing **Remove
  selected**. Once the removal finishes, automatic cleanup is turned on for the ticked
  rows. The line under the box names everything the schedule will then remove, and
  how old it must be and how often it runs. It only adds to the schedule, it never
  turns anything off. If the schedule already covers your rows, the dialog says so
  and offers **Change timing**.

**Gotchas**

- **Nothing a stack uses is listed.** The server builds the lists and checks every
  item again just before it removes it. An item that came into use in the meantime
  is skipped, and the result says how many were skipped or failed.
- **Volumes and networks are off by default.** A volume can hold a database or
  uploads, and removing it cannot be undone, so back up first if you are unsure.
  Networks use no disk. Turning on automatic removal of volumes asks you to confirm
  first.
- **A stack whose compose file cannot be read hides the lists.** The dialog names
  the stack. Until Capstan can read its compose file, only build cache is listed,
  because that stack could use any image, container, volume or network.
- **Image sizes are an estimate.** Each image counts only the layers no other image
  shares, so removing several images can free more than the total shown.

## Automatic cleanup

The **Automatic cleanup** panel on this page removes unused Docker data on a
schedule. It is off until you turn it on. By default it runs every 24 h and removes
dangling images and build cache older than 168 h. The panel reads its sentence from
the saved schedule, so it always names what will actually be removed and what never is.

**Common tasks**

- **Turn it on:** use **Turn on**, check the settings in the dialog and save.
- **Choose what it removes:** use **Edit schedule**. There is one switch each for
  dangling images, unused tagged images, build cache, stopped containers outside any
  stack, unattached volumes and unused networks. Dangling images and build cache are
  on by default; the rest are off.
- **Change timing:** in **Edit schedule**, set how often it runs and the age floor.
- **Set the dashboard warning:** in **Edit schedule**, set how many GB must be
  freeable before the dashboard warns you. 0 turns the warning off.
- **See what the next run would remove:** use **Preview next run**. It removes nothing.
- **Run it once now:** use **Run now** and confirm. It runs with the saved settings,
  even when the schedule is off.
- **Review past runs:** open **Run history** under the panel. Each run shows what it
  removed and how much space it freed. A skipped or failed run says why.

**Gotchas**

- **Stack objects are never removed.** Anything a stack uses or owns is left alone,
  the same rule as **Free up space**.
- **Unused tagged images can include your rollback image.** An image's age is its
  build time, not when it was pulled, so a just-pulled replacement of an old image
  already counts as old. That is why this switch is off by default and warns you
  when you turn it on. Images from a registry can be pulled again.
- **Removed volumes cannot be recovered.** Turning volumes on asks you to confirm.
- **A run can be skipped.** If a stack operation is running when it needs to remove
  more than dangling images and build cache, the run removes nothing, says why in
  the history, and tries again next time.
- **A stack whose compose file cannot be read** blocks removal of tagged images,
  containers, volumes and networks until Capstan can read it.

### Age floor

The minimum age, in hours, something must have before cleanup may remove it. It
protects fresh builds and anything you just created.

- **Age means creation time,** not last use.
- **The form enforces a minimum** and tells you what it is.

## Containers

Every container on the host, split into **Stack containers** (started by a
Capstan-managed compose project) and **Standalone containers** (started some other
way). Each row shows memory, network, disk and uptime, restart count and
auto-update state.

**Common tasks**

- **Inspect a container:** use the inspect icon for its configuration and details.
- **Start, stop, restart or pull:** use the row icons. Pull downloads the newest
  image without restarting anything.
- **Clear stopped containers:** use **Prune**.

**Gotchas**

- **Remove and Prune are permanent.** Removing a container deletes it. Prune
  removes all stopped containers on the host, including ones outside Capstan.
- **Stopped containers have no live stats.** Their CPU and memory cells stay empty.
- **Prefer the stack page for stack containers.** Acting on one container of a
  compose project works, but a stack-level start or restart keeps the whole
  project consistent.

## Images

The Docker images stored on the host, with size, age and how many containers use
each one. The page also shows how much a scheduled cleanup would reclaim.

**Common tasks**

- **Find large images:** sort by size.
- **Remove one image:** use the remove icon on its row.
- **Reclaim space in bulk:** use **Prune**. By default it removes dangling images,
  the untagged leftovers of rebuilds. Enable the "all unused" option to remove every
  image that no container uses, and set an age to spare recent ones.
- **Schedule it:** follow the Cleanup settings link. See
  [Settings help](settings.md#docker-cleanup).

**Gotchas**

- **"All unused" is broad.** It also removes images you pulled for a stack that is
  currently stopped. They download again on the next start.
- **An image a container still uses is not pruned.** Remove or stop that container
  first.

## Volumes

Docker volumes: the storage containers use for data that must outlive them, such as
databases. Each row shows the driver, size, the stack that uses it and where it lives
on disk.

**Common tasks**

- **See what a stack stores:** read the Stack column.
- **Spot unused volumes:** a volume with no container attached is unused.
- **Remove a volume:** use the remove icon, or **Prune** for all unused volumes.

**Gotchas**

- **Removing a volume deletes its data for good.** There is no undo.
- **Size can read "size unknown".** That means Docker gave no figure for the
  volume, not that it is empty.
- **Capstan backups copy the stack directory.** A named Docker volume is not part of
  that copy. See [what gets backed up](../how-to/configure-backups.md#what-gets-backed-up).

## Networks

Private Docker networks that let the containers of a stack reach each other. Each
stack gets its own. Rows show the driver, scope, whether the network is internal,
how many containers are attached and the stack it belongs to.

**Common tasks**

- **Create a network:** use **Create** when containers from several stacks need to
  share one.
- **Remove unused networks:** use **Prune**.

**Gotchas**

- **Built-in networks stay.** The bridge, host and none networks cannot be removed.
- **A network in use will not delete.** Detach or stop its containers first.

## Build cache

Layers Docker saved while building images, so repeat builds run faster. Rows show the
type, a description, size, when the entry was last used and whether a build is using it.

**Common tasks**

- **Free disk space:** use **Prune**. Set an age to keep recent entries.
- **Find the biggest entries:** sort by size.

**Gotchas**

- **Clearing it is safe, but the next build is slower.** Docker rebuilds whatever it
  needs.
- **The "all" option also drops reusable cache.** The default removes only entries
  that nothing is using.

---

[← Documentation index](../../README.md#documentation)
