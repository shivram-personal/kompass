/**
 * Computes a 1- or 2-character avatar label for a username.
 *
 * Rules:
 *   - operate on the local-part (before any '@') only — domains
 *     never carry useful identity for an in-app avatar.
 *   - if the local-part contains separators (`.`, `_`, `-`), use the
 *     first letter of each segment (max 2). e.g. "mary.kohli" → "MK".
 *   - otherwise, use the first 1-2 letters of the whole local-part.
 *     e.g. "mkohli" → "MK".
 *   - always uppercase.
 *   - returns '' for inputs with no usable letters so the caller can
 *     decide on a graceful fallback (silhouette icon, '?').
 *
 * Without the fallback to the leading letters, usernames like
 * "mkohli" produced no segment initials, radar's UserMenu showed a
 * generic silhouette, and the user perceived duplicated identity
 * affordance because another circle in the header (radar-hub-web's
 * own avatar) showed correctly-computed initials. (SKY-825 bug 41)
 */
export function computeUserInitials(username: string | null | undefined): string {
  if (!username) return ''
  const localPart = username.split('@')[0]
  if (!localPart) return ''
  const segments = localPart.split(/[._-]/).filter(Boolean)
  // 2+ segments → use the first letter of each (e.g. "mary.kohli" → "MK").
  // Otherwise fall back to the leading letters of the whole local-part
  // so single-segment usernames like "mkohli" still produce "MK"
  // instead of just "M". (SKY-825 bug 41)
  if (segments.length >= 2) {
    return segments
      .slice(0, 2)
      .map(s => s[0]?.toUpperCase() ?? '')
      .filter(Boolean)
      .join('')
  }
  return localPart.slice(0, 2).toUpperCase()
}
