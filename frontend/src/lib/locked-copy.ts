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
