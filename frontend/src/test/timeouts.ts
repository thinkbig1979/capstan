// Budget for an App-level findBy*/waitFor: the first full render of <App />
// (boot probes, route tree, redirect) on a loaded CI runner. Testing Library's
// 1000ms default failed there at 1798ms (agent-os-g0yl, main run 38081984922);
// the same tests are green in ~1s on a quiet machine. This is a ceiling, not a
// delay: findBy* returns as soon as the element appears, so a passing test
// costs nothing extra and only a real failure waits the full budget.
export const APP_FIND_TIMEOUT = 5000
