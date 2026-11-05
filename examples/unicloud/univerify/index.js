'use strict'

const crypto = require('node:crypto')
const MAX_BODY = 32 * 1024

function reply(statusCode, code, extra = {}) {
  return {
    mpserverlessComposedResponse: true,
    isBase64Encoded: false,
    statusCode,
    headers: {
      'content-type': 'application/json; charset=utf-8',
      'cache-control': 'no-store'
    },
    body: JSON.stringify({ code, ...extra })
  }
}

function header(headers, name) {
  if (!headers || typeof headers !== 'object' || Array.isArray(headers)) return null
  const matches = Object.entries(headers).filter(([key]) => key.toLowerCase() === name)
  return matches.length === 1 && typeof matches[0][1] === 'string' ? matches[0][1] : null
}

function keyID(value) {
  return typeof value === 'string' && value.trim() === value && /^[a-zA-Z0-9_-]{1,64}$/.test(value)
}

function config() {
  const appid = process.env.UV_APP_ID
  if (!appid || appid.trim() !== appid) return null
  try {
    const source = JSON.parse(process.env.UV_SIGNING_KEYS)
    if (!source || typeof source !== 'object' || Array.isArray(source)) return null
    const entries = Object.entries(source)
    if (entries.length < 1 || entries.length > 2) return null
    const keys = new Map()
    for (const [id, encoded] of entries) {
      if (!keyID(id) || typeof encoded !== 'string') return null
      const key = Buffer.from(encoded, 'base64')
      if (key.length !== 32 || key.toString('base64') !== encoded) return null
      keys.set(id, key)
    }
    return { appid, keys }
  } catch {
    return null
  }
}

function validCredential(value, maxBytes) {
  return typeof value === 'string' && value.length > 0 &&
    Buffer.byteLength(value, 'utf8') <= maxBytes && Buffer.from(value, 'utf8').toString('utf8') === value
}

function validPhone(value) {
  return typeof value === 'string' && value.trim() === value && value.length <= 32 && /^\+?[0-9]+$/.test(value)
}

function providerCode(result) {
  if (!result || typeof result !== 'object') return {}
  const code = result.errCode ?? result.code
  return Number.isSafeInteger(code) ? { providerCode: code } : {}
}

// 仅在 HTTP 格式、服务端配置、签名、时间窗及凭证全部合法时调用取号接口。
exports.main = async function (event) {
  if (!event || typeof event !== 'object') return reply(400, 'INVALID_REQUEST')
  if (event.httpMethod !== 'POST') return reply(405, 'METHOD_NOT_ALLOWED')
  const contentType = header(event.headers, 'content-type')
  if (!contentType || /[\r\n]/.test(contentType) || !/^application\/json(?:\s*;\s*charset\s*=\s*(?:utf-8|"utf-8"))?\s*$/i.test(contentType)) {
    return reply(415, 'UNSUPPORTED_MEDIA_TYPE')
  }
  if (typeof event.body !== 'string' ||
      (event.isBase64Encoded !== undefined && typeof event.isBase64Encoded !== 'boolean')) {
    return reply(400, 'INVALID_REQUEST')
  }
  const isBase64 = event.isBase64Encoded === true
  if (Buffer.byteLength(event.body, 'utf8') > (isBase64 ? 4 * Math.ceil(MAX_BODY / 3) : MAX_BODY)) {
    return reply(413, 'PAYLOAD_TOO_LARGE')
  }
  const body = Buffer.from(event.body, isBase64 ? 'base64' : 'utf8')
  if (body.length > MAX_BODY) return reply(413, 'PAYLOAD_TOO_LARGE')
  if ((isBase64 && body.toString('base64') !== event.body) ||
      (!isBase64 && body.toString('utf8') !== event.body) ||
      !Buffer.from(body.toString('utf8'), 'utf8').equals(body)) {
    return reply(400, 'INVALID_REQUEST')
  }

  const settings = config()
  if (!settings) return reply(503, 'UNAVAILABLE')
  const id = header(event.headers, 'x-univerify-key-id')
  const timestamp = header(event.headers, 'x-univerify-timestamp')
  const sign = header(event.headers, 'x-univerify-signature')
  if (!keyID(id) || !settings.keys.has(id) || typeof timestamp !== 'string' ||
      timestamp.trim() !== timestamp || !/^[1-9][0-9]{0,15}$/.test(timestamp) ||
      typeof sign !== 'string' || sign.length !== 64 || !/^[a-f0-9]{64}$/.test(sign)) {
    return reply(401, 'UNAUTHORIZED')
  }
  const milliseconds = Number(timestamp)
  const delta = Date.now() - milliseconds
  if (!Number.isSafeInteger(milliseconds) || delta < -5000 || delta > 60000) {
    return reply(401, 'UNAUTHORIZED')
  }
  const digest = crypto.createHash('sha256').update(body).digest('hex')
  const message = ['univerify-go/v1', 'POST', id, timestamp, digest].join('\n')
  const expected = crypto.createHmac('sha256', settings.keys.get(id)).update(message, 'utf8').digest()
  if (!crypto.timingSafeEqual(expected, Buffer.from(sign, 'hex'))) return reply(401, 'UNAUTHORIZED')

  let input
  try {
    input = JSON.parse(body.toString('utf8'))
  } catch {
    return reply(400, 'INVALID_REQUEST')
  }
  if (!input || typeof input !== 'object' || Array.isArray(input) ||
      Object.keys(input).sort().join(',') !== 'access_token,openid' ||
      !validCredential(input.access_token, 4096) || !validCredential(input.openid, 1024)) {
    return reply(400, 'INVALID_REQUEST')
  }
  try {
    const result = await uniCloud.getPhoneNumber({
      provider: 'univerify',
      appid: settings.appid,
      access_token: input.access_token,
      openid: input.openid
    })
    if (!result || result.code !== 0 || !validPhone(result.phoneNumber)) {
      return reply(502, 'UPSTREAM_ERROR', providerCode(result))
    }
    return reply(200, 'OK', { phoneNumber: result.phoneNumber })
  } catch (err) {
    // 不返回或记录上游 message；其中可能包含手机号或凭证。
    return reply(502, 'UPSTREAM_ERROR', providerCode(err))
  }
}
