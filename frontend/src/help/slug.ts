/**
 * Heading -> anchor, the same rule as slugify() in scripts/check-docs.sh, which
 * Docs CI uses to validate every `#anchor` link in the repo. Keep the two
 * identical (slug.test.ts runs the bash function to prove it), including the
 * ASCII-only behaviour: non-ASCII letters are dropped, not transliterated.
 */
export function slugify(heading: string): string {
  return heading
    .toLowerCase()
    .replace(/[^a-z0-9 -]/g, '')
    .replace(/ /g, '-')
}
