/**
 * Every docs/**\/*.md file as raw text, bundled at build time (no runtime fetch).
 * Import this module only through the dynamic import() in content.ts: a static
 * import would pull ~100 KB of markdown into the entry chunk.
 *
 * The glob reaches outside the Vite root, so the Docker build must copy docs/
 * next to frontend/ (docker/Dockerfile, .dockerignore). A glob that matches
 * nothing is not a build error, it is an empty object; buildHelpLibrary()
 * rejects that so help fails visibly instead of shipping empty.
 */
export const RAW_DOCS = import.meta.glob<string>('../../../docs/**/*.md', {
  query: '?raw',
  import: 'default',
  eager: true,
})
