/**
 * Client-side validation mirroring internal/store/validate.go; the server
 * repeats these checks. These helpers only provide instant feedback.
 */

const VERSION_MAX_LENGTH = 128
const VERSION_PATTERN = /^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-((0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(\.(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*))?(\+([0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*))?$/
const PROFILE_MAX_LENGTH = 64
const PROFILE_PATTERN = /^[\p{L}\p{N}][\p{L}\p{N}._ -]{0,63}$/u
const RESERVED_PROFILE = /^(CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])(\..*)?$/i

export function validateInstanceName(name: string): string | null {
  const value = name.trim()
  if (!value) return '请输入实例名称。'
  if (Array.from(value).length > 160) return '实例名称请控制在 160 个字符以内。'
  return null
}

export function validateVersion(version: string): string | null {
  const value = version.trim()
  if (!value) return '请输入 Harness 版本。'
  if (value.length > VERSION_MAX_LENGTH) return '版本号过长。'
  if (!VERSION_PATTERN.test(value)) return '请输入完整 SemVer 版本（例如 1.2.3-rc.1）。'
  return null
}

export function validateProfile(profile: string): string | null {
  const value = profile
  if (!value.trim()) return '请输入 Profile 名称。'
  if (value !== value.trim()) return 'Profile 名称首尾不能有空格。'
  if (Array.from(value).length > PROFILE_MAX_LENGTH) return 'Profile 名称不能超过 64 个字符。'
  if (!PROFILE_PATTERN.test(value) || value.includes('..') || value.endsWith('.')
      || RESERVED_PROFILE.test(value) || value.toLowerCase() === 'node_modules') {
    return 'Profile 须以字母或数字开头，只允许字母、数字、空格、点、下划线和连字符；不支持路径符号、连续点、结尾点或系统保留名。'
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
