/** Client-side validation helpers. The server stays authoritative; these just catch typos early. */

export const USERNAME_RE = /^[a-zA-Z0-9_.]{2,64}$/

export function validateUsername(v: string): string | null {
  if (!v.trim()) return "Username is required."
  if (v.includes("-")) return "Hyphens are not allowed: they separate username parameters like -country-us."
  if (!USERNAME_RE.test(v)) return "Use 2–64 letters, numbers, dots or underscores."
  return null
}

const DOMAIN_RE = /^(\*\.)?([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$/

export function validateDomain(v: string): string | null {
  return DOMAIN_RE.test(v) ? null : `“${v}” is not a valid domain (use example.com or *.example.com).`
}

export function normalizeDomain(v: string): string {
  return v
    .trim()
    .toLowerCase()
    .replace(/^https?:\/\//, "")
    .replace(/\/.*$/, "")
    .replace(/\.$/, "")
}

const IPV4 = /^(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}$/

function isIPv6(v: string) {
  if (!/^[0-9a-f:.]+$/i.test(v) || !v.includes(":")) return false
  try {
    new URL(`http://[${v}]/`)
    return true
  } catch {
    return false
  }
}

export function isIp(v: string): boolean {
  return IPV4.test(v) || isIPv6(v)
}

export function validateCidr(v: string): string | null {
  const [ip, prefix, extra] = v.split("/")
  if (extra !== undefined || !ip) return `“${v}” is not a valid IP or CIDR.`
  const v4 = IPV4.test(ip)
  const v6 = !v4 && isIPv6(ip)
  if (!v4 && !v6) return `“${v}” is not a valid IP or CIDR.`
  if (prefix !== undefined) {
    const n = Number(prefix)
    if (!/^\d+$/.test(prefix) || n < 0 || n > (v4 ? 32 : 128)) return `“${v}” has an invalid prefix length.`
  }
  return null
}

/** WireGuard keys are 32 bytes, base64: 44 characters ending in "=". */
export function validateWireguardKey(v: string): string | null {
  const key = v.trim()
  if (!key) return "Paste the private key."
  if (key.length !== 44 || !/^[A-Za-z0-9+/]{43}=$/.test(key)) {
    return "A WireGuard private key is 44 base64 characters ending in “=”."
  }
  return null
}

const PW_ALPHABET = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"

/** URL-safe random password (no characters that need escaping in proxy URLs). */
export function generatePassword(length = 20): string {
  const bytes = new Uint32Array(length)
  crypto.getRandomValues(bytes)
  return Array.from(bytes, (b) => PW_ALPHABET[b % PW_ALPHABET.length]).join("")
}

export function validateCountryCode(v: string): string | null {
  return /^[a-z]{2}$/.test(v) ? null : "Use a two-letter country code, like us or de."
}
