'use strict'

const { test } = require('node:test')
const assert = require('node:assert/strict')
const crypto = require('node:crypto')
const fs = require('node:fs')
const path = require('node:path')
const { main } = require('./index.js')
const vectors = JSON.parse(fs.readFileSync(path.join(__dirname, '../../../testdata/signatures.json'), 'utf8'))

function event(params = { openid: 'openid', access_token: 'token' }, secret = 'test-secret') {
  const message = Object.keys(params).sort().map(key => key + '=' + params[key]).join('&')
  const sign = crypto.createHmac('sha256', secret).update(message).digest('hex')
  return {
    httpMethod: 'POST',
    headers: { 'content-type': 'application/json' },
    isBase64Encoded: false,
    body: JSON.stringify({ ...params, sign })
  }
}

test('官方 POST 云函数协议', async (t) => {
  const saved = { appid: process.env.UV_APP_ID, secret: process.env.UV_SECRET, cloud: globalThis.uniCloud }
  t.after(() => {
    for (const [name, value] of [['UV_APP_ID', saved.appid], ['UV_SECRET', saved.secret]]) {
      if (value === undefined) delete process.env[name]
      else process.env[name] = value
    }
    if (saved.cloud === undefined) delete globalThis.uniCloud
    else globalThis.uniCloud = saved.cloud
  })
  let calls = 0
  t.beforeEach(() => {
    calls = 0
    process.env.UV_APP_ID = 'test-app'
    process.env.UV_SECRET = 'test-secret'
    globalThis.uniCloud = { getPhoneNumber: async (args) => {
      calls++
      assert.equal(args.appid, 'test-app')
      assert.equal(args.provider, 'univerify')
      return { code: 0, message: '', phoneNumber: '13800000000' }
    } }
  })

  await t.test('普通与 Base64 正文直接返回官方结果', async () => {
    for (const encoded of [false, true]) {
      const e = event()
      if (encoded) { e.body = Buffer.from(e.body).toString('base64'); e.isBase64Encoded = true }
      assert.deepEqual(await main(e), { code: 0, message: '', phoneNumber: '13800000000' })
    }
    assert.equal(calls, 2)
  })

  for (const vector of vectors) {
    await t.test('原始值签名 ' + vector.name, async () => {
      process.env.UV_SECRET = vector.secret
      const input = { openid: vector.openid, access_token: vector.access_token }
      const e = event(input, vector.secret)
      const expected = crypto.createHmac('sha256', vector.secret).update(vector.message).digest('hex')
      assert.equal(JSON.parse(e.body).sign, expected)
      globalThis.uniCloud.getPhoneNumber = async (args) => {
        calls++
        assert.equal(args.access_token, vector.access_token)
        assert.equal(args.openid, vector.openid)
        return { code: 0, phoneNumber: '13800000000' }
      }
      assert.equal((await main(e)).code, 0)
      assert.equal(calls, 1)
    })
  }

  const invalid = [
    ['GET', e => { e.httpMethod = 'GET' }],
    ['缺少正文', e => { delete e.body }],
    ['错误媒体类型', e => { e.headers['content-type'] = 'text/plain' }],
    ['重复媒体类型', e => { e.headers['Content-Type'] = 'application/json' }],
    ['无效 Base64 标志', e => { e.isBase64Encoded = 'true' }],
    ['无效 Base64', e => { e.isBase64Encoded = true; e.body = '!!!!' }],
    ['无效 UTF-8', e => { e.isBase64Encoded = true; e.body = Buffer.from([255]).toString('base64') }],
    ['过大正文', e => { e.body = 'x'.repeat(32769) }],
    ['非法 JSON', e => { e.body = '{' }],
    ['null', e => { e.body = 'null' }],
    ['数组', e => { e.body = '[]' }],
    ['缺少 sign', e => { const p = JSON.parse(e.body); delete p.sign; e.body = JSON.stringify(p) }],
    ['无效 sign', e => { const p = JSON.parse(e.body); p.sign = '0'.repeat(64); e.body = JSON.stringify(p) }],
    ['篡改凭证', e => { e.body = e.body.replace('token', 'other') }],
    ['多余字段', e => { const p = JSON.parse(e.body); p.appid = 'other'; e.body = JSON.stringify(p) }]
  ]
  for (const [name, mutate] of invalid) {
    await t.test(name, async () => {
      const e = event()
      mutate(e)
      await assert.rejects(main(e))
      assert.equal(calls, 0)
    })
  }

  for (const input of [
    { access_token: '', openid: 'o' },
    { access_token: 't'.repeat(4097), openid: 'o' },
    { access_token: 't', openid: 'o'.repeat(1025) },
    { access_token: '\ud800', openid: 'o' }
  ]) {
    await t.test('非法凭证', async () => {
      await assert.rejects(main(event(input)), { message: '无效请求' })
      assert.equal(calls, 0)
    })
  }

  for (const name of ['UV_APP_ID', 'UV_SECRET']) {
    await t.test('缺失配置 ' + name, async () => {
      delete process.env[name]
      await assert.rejects(main(event()), { message: '配置不可用' })
      assert.equal(calls, 0)
    })
  }

  await t.test('边界长度与 JSON 空白', async () => {
    const e = event({ access_token: 't'.repeat(4096), openid: 'o'.repeat(1024) })
    e.body += ' '.repeat(32768 - Buffer.byteLength(e.body))
    assert.equal((await main(e)).code, 0)
    assert.equal(calls, 1)
  })

  await t.test('直接返回非零官方 code', async () => {
    globalThis.uniCloud.getPhoneNumber = async () => ({ code: 5000, message: 'provider failure' })
    assert.deepEqual(await main(event()), { code: 5000, message: 'provider failure' })
  })

  await t.test('上游异常原样传递', async () => {
    const error = new Error('provider-original-error')
    error.code = 5000
    globalThis.uniCloud.getPhoneNumber = async () => { throw error }
    await assert.rejects(main(event()), actual => actual === error)
  })

  await t.test('签名没有一次性语义', async () => {
    const e = event()
    assert.equal((await main(e)).code, 0)
    assert.equal((await main(e)).code, 0)
    assert.equal(calls, 2)
  })
})
