import type { AppSettings } from "@/lib/types"

export interface ProxyUrl {
  kind: "https" | "http" | "socks5h"
  label: string
  url: string
}

export const PASSWORD_PLACEHOLDER = "PASSWORD"

function hostPort(host: string, port: number, defaultPort: number) {
  return port === defaultPort ? host : `${host}:${port}`
}

/** Ready-made proxy URLs for a user, built from AppSettings as described in docs/api.md. */
export function buildProxyUrls(app: AppSettings | undefined, username: string, password?: string): ProxyUrl[] {
  const host = app?.publicProxyHost?.trim() || window.location.hostname
  const auth = `${encodeURIComponent(username)}:${encodeURIComponent(password || PASSWORD_PLACEHOLDER)}`
  const urls: ProxyUrl[] = []
  const httpsPort = app?.publicHttpsPort ?? 0
  const httpPort = app?.publicHttpPort ?? 0
  if (httpsPort > 0) {
    urls.push({ kind: "https", label: "HTTPS proxy (TLS)", url: `https://${auth}@${hostPort(host, httpsPort, 443)}` })
  }
  if (httpPort > 0) {
    urls.push({ kind: "http", label: "HTTP proxy", url: `http://${auth}@${host}:${httpPort}` })
    urls.push({ kind: "socks5h", label: "SOCKS5 (remote DNS)", url: `socks5h://${auth}@${host}:${httpPort}` })
  }
  return urls
}

export function usernameExamples(username: string) {
  return [
    { value: `${username}-country-us`, description: "Only exit through lanes in a country (ISO code)." },
    { value: `${username}-session-abc123`, description: "Sticky lane for this session ID (10 min by default)." },
    { value: `${username}-sessttl-30`, description: "Sticky session lifetime in minutes (use with -session-)." },
    { value: `${username}-country-de-session-job42-sessttl-30`, description: "Parameters can be combined." },
  ]
}

export function curlExample(url: ProxyUrl | undefined): string {
  if (!url) return "# Set a public proxy host and port in Settings → App first."
  return `curl -x '${url.url}' https://api.ipify.org`
}
