/**
 * What a session with env values locked can and cannot do with compose text
 * (agent-os-ly6i). The server refuses save, lint and stack create while
 * locked: their replies pass the caller's own text through the secret
 * masking, and where a "***" lands in a list of guesses would show which guess
 * is the secret.
 */
export const LOCKED_COMPOSE_NOTICE =
  'Env values are locked. You can read this file with secrets hidden. Saving, linting and creating stacks need an unlock, because the replies could show which text matches a secret.'

export const LOCKED_NEW_STACK_NOTICE =
  'Env values are locked. Linting and creating a stack need an unlock, because the replies could show which text matches a secret.'

/**
 * What a locked session cannot do with backups (agent-os-h71i). The server
 * refuses changes to where backups go or what they keep, initialising a
 * repository and restores: a locked session that could point the repository
 * at a place it controls would read every stack's .env.
 */
export const LOCKED_BACKUP_NOTICE =
  'Env values are locked. Changing the repository, its password, the cloud remote or retention, initializing a repository and restoring need an unlock, because backups hold every stack\'s .env.'

export const LOCKED_RESTORE_NOTICE =
  'Env values are locked. Restoring a snapshot needs an unlock, because a restore writes files into the stack.'

/**
 * What a locked session cannot do with git credentials (agent-os-mu4z): the
 * server refuses a change to the SSH key, HTTPS user or token, global or per
 * folder, because pushes and pulls use them.
 */
export const LOCKED_GIT_CREDENTIALS_NOTICE =
  'Env values are locked. Changing the SSH key, HTTPS username or token needs an unlock, because pushes and pulls use them. The commit author does too, because a locked history masks it with your secrets.'

export const LOCKED_FOLDER_CREDENTIALS_NOTICE =
  'Env values are locked. Changing this folder\'s git credentials needs an unlock, because pushes and pulls use them.'

/**
 * Why Pull is off while locked (agent-os-hblm): the server refuses a pull,
 * because it lands the remote's text in the stack folder, where a locked view
 * masks it, and a redeploy runs it.
 */
export const LOCKED_PULL_NOTICE =
  "Env values are locked. Pulling needs an unlock, because a pull writes the remote's files into your stacks and a redeploy runs them."

/**
 * Why Commit is off while locked (agent-os-xmbg): a locked log masks the
 * commit message with the stack's secrets, so the server refuses a commit.
 */
export const LOCKED_COMMIT_NOTICE =
  'Env values are locked. Committing needs an unlock, because the history masks commit messages with your secrets. Pushing stays available.'

/** Tooltip on a credential control disabled while locked. */
export const LOCKED_CREDENTIAL_TITLE = 'Unlock env values to change this'

/** Tooltip on a backup control disabled while locked. */
export const LOCKED_BACKUP_TITLE = 'Unlock env values to change this'
