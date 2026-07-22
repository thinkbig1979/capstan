// Inactivity warning fires after this many minutes with no terminal input;
// a further 5-minute grace period and 60-second countdown follow before the
// session is force-disconnected — see useInactivityTimer.
export const SESSION_WARNING_MINUTES = 25

export const FONT_SIZE_KEY = 'terminal-font-size'
export const DEFAULT_FONT_SIZE = 14
export const MIN_FONT_SIZE = 10
export const MAX_FONT_SIZE = 24

export const HINTS_SHOWN_KEY = 'terminal-hints-shown'
