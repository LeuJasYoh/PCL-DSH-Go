/**
 * Client-side validation with the exact Chinese wording used by the desktop
 * launcher (ShellView.editInstance validation + Controller.saveSettings).
 * The server repeats these checks; these helpers only provide instant feedback.
 */

const VERSION_PATTERN = /^[0-9][0-9A-Za-z.+-]{0,79}$/
const PROFILE_PATTERN = /^[\p{L}\p{N}][\p{L}\p{N}._ -]{0,127}$/u
const RESERVED_PROFILE = /^(CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])(\..*)?$/i

export function validateInstanceName(name: string): string | null {
  const value = name.trim()
  if (!value) return '请输入实例名称。'
  if (value.length > 160) return '实例名称请控制在 160 个字符以内。'
  return null
}

export function validateVersion(version: string): string | null {
  const value = version.trim()
  if (!value) return '请输入 Harness 版本。'
  if (!VERSION_PATTERN.test(value)) return '请输入精确版本号，例如 0.1.0-rc.6。'
  return null
}

export function validateProfile(profile: string): string | null {
  const value = profile.trim()
  if (!value) return '请输入 Profile 名称。'
  if (!PROFILE_PATTERN.test(value) || value.includes('..') || value.endsWith('.') || value.endsWith(' ')
      || RESERVED_PROFILE.test(value)) {
    return 'Profile 名称须为 1–128 个字符，可使用中文、数字、空格、点、下划线或连字符；不支持路径符号、连续点和系统保留名。'
  }
  return null
}

/** Empty port text means "auto assign"; invalid text returns the desktop message. */
export function validatePort(text: string): string | null {
  const value = text.trim()
  if (!value) return null
  if (!/^[0-9]+$/.test(value)) return '请输入有效端口。'
  const port = Number(value)
  if (port < 1024 || port > 65535) return '端口必须在 1024–65535 之间。'
  return null
}

export function parsePort(text: string): number | null {
  const value = text.trim()
  return value ? Number(value) : null
}

function parseHttpUrl(value: string): URL | null {
  try {
    return new URL(value)
  } catch {
    return null
  }
}

/** Mirrors Controller.saveSettings validation and message. */
export function validateSettings(proxy: string, registry: string): string | null {
  const registryUrl = parseHttpUrl(registry.trim())
  if (!registryUrl || !['https:', 'http:'].includes(registryUrl.protocol) || !registryUrl.hostname
      || registryUrl.username || registryUrl.password) {
    return 'Registry 须为 HTTP(S) 地址；代理请填写如 http://127.0.0.1:7890，且不包含凭据。'
  }
  const proxyValue = proxy.trim()
  if (!proxyValue) return null
  const proxyUrl = parseHttpUrl(proxyValue)
  if (!proxyUrl || proxyUrl.protocol !== 'http:' || !proxyUrl.hostname || proxyUrl.username
      || proxyUrl.password || !proxyUrl.port || Number(proxyUrl.port) < 1 || Number(proxyUrl.port) > 65535) {
    return 'Registry 须为 HTTP(S) 地址；代理请填写如 http://127.0.0.1:7890，且不包含凭据。'
  }
  return null
}
