'use strict'

const { test } = require('node:test')
const assert = require('node:assert/strict')
const crypto = require('node:crypto')
const fs = require('node:fs')
const path = require('node:path')
const { main } = require('./index.js')
const vectors = JSON.parse(fs.readFileSync(path.join(__dirname, '../../../testdata/signatures.json'), 'utf8'))
const key = Buffer.alloc(32)
const now = 1793872800000

function event(body = JSON.stringify({ access_token: 'token', openid: 'openid' }), timestamp = String(now), id = 'test', secret = key) {
  const digest = crypto.createHash('sha256').update(body).digest('hex')
  const signature = crypto.createHmac('sha256', secret)
    .update(['univerify-go/v1', 'POST', id, timestamp, digest].join('\n')).digest('hex')
  return {
    httpMethod: 'POST', isBase64Encoded: false, body,
    headers: {
      'content-type': 'application/json',
      'x-univerify-key-id': id,
      'x-univerify-timestamp': timestamp,
      'x-univerify-signature': signature
    }
  }
}

test('云函数协议', async (t) => {
  const saved = { appid: process.env.UV_APP_ID, keys: process.env.UV_SIGNING_KEYS, cloud: globalThis.uniCloud, now: Date.now }
  t.after(() => {
    for (const [name, value] of [['UV_APP_ID', saved.appid], ['UV_SIGNING_KEYS', saved.keys]]) {
      if (value === undefined) delete process.env[name]
      else process.env[name] = value
    }
    if (saved.cloud === undefined) delete globalThis.uniCloud
    else globalThis.uniCloud = saved.cloud
    Date.now = saved.now
  })
  let calls = 0
  t.beforeEach(() => {
    calls = 0
    Date.now = () => now
    process.env.UV_APP_ID = 'test-app'
    process.env.UV_SIGNING_KEYS = JSON.stringify({ test: key.toString('base64') })
    globalThis.uniCloud = { getPhoneNumber: async (args) => {
      calls++
      assert.equal(args.appid, 'test-app')
      assert.equal(args.provider, 'univerify')
      return { code: 0, phoneNumber: '13800000000' }
    } }
  })

  await t.test('有效请求及 Base64 事件', async () => {
    for (const base64 of [false, true]) {
      const e = event()
      if (base64) { e.body = Buffer.from(e.body).toString('base64'); e.isBase64Encoded = true }
      const result = await main(e)
      assert.equal(result.statusCode, 200)
      assert.equal(result.mpserverlessComposedResponse, true)
      assert.equal(result.headers['cache-control'], 'no-store')
      assert.deepEqual(JSON.parse(result.body), { code: 'OK', phoneNumber: '13800000000' })
    }
    assert.equal(calls, 2)
  })

  for (const vector of vectors) {
    await t.test(`跨语言向量 ${vector.name}`, async () => {
      process.env.UV_SIGNING_KEYS = JSON.stringify({ [vector.key_id]: vector.key })
      const e = event(vector.body, vector.timestamp, vector.key_id, Buffer.from(vector.key, 'base64'))
      assert.equal(e.headers['x-univerify-signature'], vector.signature)
      assert.equal((await main(e)).statusCode, 200)
      assert.equal(calls, 1)
    })
  }

  for (const delta of [-5001, -5000, 60000, 60001]) {
    await t.test(`时间边界 ${delta}`, async () => {
      const result = await main(event(undefined, String(now - delta)))
      const accepted = delta >= -5000 && delta <= 60000
      assert.equal(result.statusCode, accepted ? 200 : 401)
      assert.equal(calls, accepted ? 1 : 0)
    })
  }

  const invalid = [
    ['GET', (e) => { e.httpMethod = 'GET' }, 405],
    ['无媒体类型', (e) => { delete e.headers['content-type'] }, 415],
    ['错误媒体类型', (e) => { e.headers['content-type'] = 'text/plain' }, 415],
    ['媒体类型换行', (e) => { e.headers['content-type'] += '\n' }, 415],
    ['缺正文', (e) => { delete e.body }, 400],
    ['错误 Base64 标志', (e) => { e.isBase64Encoded = 'true' }, 400],
    ['无效 Base64', (e) => { e.isBase64Encoded = true; e.body = '!!!!' }, 400],
    ['无效 UTF8', (e) => { e.isBase64Encoded = true; e.body = Buffer.from([255]).toString('base64') }, 400],
    ['过大正文', (e) => { e.body = 'x'.repeat(32769) }, 413],
    ['未知密钥', (e) => { e.headers['x-univerify-key-id'] = 'unknown' }, 401],
    ['密钥编号换行', (e) => { e.headers['x-univerify-key-id'] += '\n' }, 401],
    ['重复请求头', (e) => { e.headers['X-Univerify-Key-Id'] = 'test' }, 401],
    ['数组请求头', (e) => { e.headers['x-univerify-key-id'] = ['test'] }, 401],
    ['缺签名', (e) => { delete e.headers['x-univerify-signature'] }, 401],
    ['篡改签名', (e) => { e.headers['x-univerify-signature'] = '0'.repeat(64) }, 401],
    ['签名换行', (e) => { e.headers['x-univerify-signature'] += '\n' }, 401],
    ['篡改正文', (e) => { e.body = e.body.replace('token', 'other') }, 401],
    ['时间戳指数', (e) => { e.headers['x-univerify-timestamp'] = '1e12' }, 401],
    ['时间戳换行', (e) => { e.headers['x-univerify-timestamp'] += '\n' }, 401]
  ]
  for (const [name, mutate, status] of invalid) {
    await t.test(name, async () => {
      const e = event()
      mutate(e)
      assert.equal((await main(e)).statusCode, status)
      assert.equal(calls, 0)
    })
  }

  for (const [name, body] of [
    ['坏 JSON', '{'], ['null', 'null'], ['数组', '[]'], ['字段缺失', '{}'],
    ['额外 appid', JSON.stringify({ access_token: 'token', openid: 'openid', appid: 'attacker-app' })],
    ['空凭证', JSON.stringify({ access_token: '', openid: 'openid' })],
    ['过长 token', JSON.stringify({ access_token: 't'.repeat(4097), openid: 'openid' })],
    ['过长 openid', JSON.stringify({ access_token: 't', openid: 'o'.repeat(1025) })],
    ['未配对代理字符', '{"access_token":"\\ud800","openid":"o"}']
  ]) {
    await t.test(name, async () => {
      assert.equal((await main(event(body))).statusCode, 400)
      assert.equal(calls, 0)
    })
  }

  await t.test('大小边界与头大小写', async () => {
    let body = JSON.stringify({ access_token: 't'.repeat(4096), openid: 'o'.repeat(1024) })
    body += ' '.repeat(32768 - Buffer.byteLength(body))
    const e = event(body)
    e.headers['X-Univerify-Key-Id'] = e.headers['x-univerify-key-id']
    delete e.headers['x-univerify-key-id']
    assert.equal((await main(e)).statusCode, 200)
    assert.equal(calls, 1)
  })

  for (const encoded of ['', '{}', '{', '[]', JSON.stringify({ test: 'invalid' }), JSON.stringify({ test: key.toString('base64') + '\n' })]) {
    await t.test(`非法密钥配置 ${encoded.length}`, async () => {
      process.env.UV_SIGNING_KEYS = encoded
      assert.equal((await main(event())).statusCode, 503)
      assert.equal(calls, 0)
    })
  }

  await t.test('缺少 AppID', async () => {
    delete process.env.UV_APP_ID
    assert.equal((await main(event())).statusCode, 503)
    assert.equal(calls, 0)
  })

  await t.test('双密钥轮换', async () => {
    process.env.UV_SIGNING_KEYS = JSON.stringify({ test: key.toString('base64'), next: Buffer.alloc(32, 1).toString('base64') })
    assert.equal((await main(event())).statusCode, 200)
    assert.equal((await main(event(undefined, String(now), 'next', Buffer.alloc(32, 1)))).statusCode, 200)
    assert.equal(calls, 2)
  })

  await t.test('上游数字错误码，无敏感错误文案', async () => {
    globalThis.uniCloud.getPhoneNumber = async () => { calls++; throw { errCode: 5000, message: 'private-token-and-phone' } }
    const result = await main(event())
    assert.equal(result.statusCode, 502)
    assert.deepEqual(JSON.parse(result.body), { code: 'UPSTREAM_ERROR', providerCode: 5000 })
    assert.equal(calls, 1)
    assert.equal(result.body.includes('private'), false)
  })

  await t.test('上游错误形状或手机号不合法', async () => {
    for (const result of [null, { code: 0 }, { code: 0, phoneNumber: '+' }, { code: 0, phoneNumber: '13800000000\n' }, { code: 3, phoneNumber: '13800000000' }]) {
      globalThis.uniCloud.getPhoneNumber = async () => result
      assert.equal((await main(event())).statusCode, 502)
    }
  })

  await t.test('窗口内重放是已记录的限制', async () => {
    const e = event()
    assert.equal((await main(e)).statusCode, 200)
    assert.equal((await main(e)).statusCode, 200)
    assert.equal(calls, 2)
  })
})
