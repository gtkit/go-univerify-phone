'use strict'

const crypto = require('node:crypto')
const MAX_BODY = 32 * 1024

function validCredential(value, maxBytes) {
  return typeof value === 'string' && value.length > 0 &&
    Buffer.byteLength(value, 'utf8') <= maxBytes && Buffer.from(value, 'utf8').toString('utf8') === value
}

// 对接官方 POST 示例：校验原始参数的 sign 后，直接返回取号结果。
exports.main = async function (event) {
  if (!event || event.httpMethod !== 'POST') throw new Error('仅支持 POST')
  const headers = Object.entries(event.headers || {})
    .filter(([name]) => name.toLowerCase() === 'content-type')
  const contentType = headers.length === 1 ? headers[0][1] : ''
  if (typeof contentType !== 'string' || /[\r\n]/.test(contentType) ||
      !/^application\/json(?:\s*;\s*charset\s*=\s*(?:utf-8|"utf-8"))?\s*$/i.test(contentType)) {
    throw new Error('无效请求')
  }
  if (typeof event.body !== 'string' ||
      (event.isBase64Encoded !== undefined && typeof event.isBase64Encoded !== 'boolean')) {
    throw new Error('无效请求')
  }
  const base64 = event.isBase64Encoded === true
  if (Buffer.byteLength(event.body, 'utf8') > (base64 ? 4 * Math.ceil(MAX_BODY / 3) : MAX_BODY)) {
    throw new Error('无效请求')
  }
  const body = Buffer.from(event.body, base64 ? 'base64' : 'utf8')
  if (body.length > MAX_BODY ||
      (base64 && body.toString('base64') !== event.body) ||
      (!base64 && body.toString('utf8') !== event.body) ||
      !Buffer.from(body.toString('utf8'), 'utf8').equals(body)) {
    throw new Error('无效请求')
  }
  let input
  try {
    input = JSON.parse(body.toString('utf8'))
  } catch {
    throw new Error('无效请求')
  }
  if (!input || typeof input !== 'object' || Array.isArray(input) ||
      Object.keys(input).sort().join(',') !== 'access_token,openid,sign' ||
      !validCredential(input.access_token, 4096) || !validCredential(input.openid, 1024)) {
    throw new Error('无效请求')
  }
  const appid = process.env.UV_APP_ID
  const secret = process.env.UV_SECRET
  if (!appid || appid.trim() !== appid || !secret ||
      Buffer.from(secret, 'utf8').toString('utf8') !== secret) {
    throw new Error('配置不可用')
  }
  const { sign, ...paramsz } = input
  if (typeof sign !== 'string' || !/^[a-f0-9]{64}$/.test(sign)) throw new Error('非法访问')
  const signStr = Object.keys(params).sort().map(key => key + '=' + params[key]).join('&')
  const expected = crypto.createHmac('sha256', secret).update(signStr, 'utf8').digest()
  if (!crypto.timingSafeEqual(expected, Buffer.from(sign, 'hex'))) throw new Error('非法访问')
  return uniCloud.getPhoneNumber({
    provider: 'univerify',
    appid,
    access_token: params.access_token,
    openid: params.openid
  })
}
