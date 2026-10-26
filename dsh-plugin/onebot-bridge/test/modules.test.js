// 模块宿主：启停 / 开关 / 作用域回收 / 出错隔离。
//
// 这一层是「能力可独立开关」的地基，所以单独测：一个模块被停掉时，
// 它注册的一切（监听器、定时器、连接）必须一起消失，而且不能连累别的模块。

import test from 'node:test'
import assert from 'node:assert/strict'
import { createModules, createBus, createScope } from '../src/modules.js'

const silentLog = { info() {}, warn() {}, error() {}, debug() {} }

test('作用域：倒序回收，重复 dispose 安全，非函数忽略', () => {
  const order = []
  const log = { ...silentLog }
  const scope = createScope('t', log)
  scope.add(() => order.push(1))
  scope.add(() => order.push(2))
  scope.add('not-a-function')
  assert.equal(scope.size, 2)
  scope.dispose()
  assert.deepEqual(order, [2, 1])
  scope.dispose()
  assert.deepEqual(order, [2, 1])
})

test('总线：监听器能摘，某个监听器抛错不影响其它监听器', () => {
  const bus = createBus({ log: silentLog })
  const seen = []
  const offA = bus.on('x', () => {
    throw new Error('boom')
  })
  bus.on('x', (v) => seen.push(v))
  bus.emit('x', 1)
  assert.deepEqual(seen, [1])
  offA()
  bus.emit('x', 2)
  assert.deepEqual(seen, [1, 2])
  assert.equal(bus.count('x'), 1)
})

test('启停：start 建实例并登记，stop 撤掉它注册的一切（监听器 + 定时器）', async () => {
  const log = silentLog
  const mods = createModules({ ctx: {}, log })
  const events = []
  let timer = null

  mods.register({
    id: 'demo',
    title: '演示模块',
    factory({ bus, scope }) {
      scope.add(bus.on('ping', (v) => events.push(`handler:${v}`)))
      timer = setInterval(() => {}, 1000)
      timer.unref?.()
      scope.add(() => clearInterval(timer))
      return {
        start() {
          events.push('start')
        },
        stop() {
          events.push('stop')
        },
        hello: () => 'hi',
      }
    },
  })

  const getConfig = () => ({ modules: { demo: true } })
  await mods.sync(getConfig)
  assert.equal(mods.isRunning('demo'), true)
  assert.equal(mods.service('demo').hello(), 'hi')

  mods.emit('ping', 1)
  assert.deepEqual(events, ['start', 'handler:1'])

  await mods.stop('demo')
  assert.equal(mods.isRunning('demo'), false)
  assert.equal(mods.service('demo'), null)
  assert.ok(events.includes('stop'))
  // 监听器摘掉了：再 emit 不再进 handler
  mods.emit('ping', 2)
  assert.deepEqual(events, ['start', 'handler:1', 'stop'])
})

test('开关：cfg.modules[id] === false 就停；改回 true 再起（sync 幂等）', async () => {
  const mods = createModules({ ctx: {}, log: silentLog })
  let started = 0
  mods.register({ id: 'a', factory: () => ({ start: () => started++ }) })
  mods.register({ id: 'b', factory: () => ({ start: () => started++ }) })

  let cfg = { modules: { a: true, b: true } }
  await mods.sync(() => cfg)
  assert.equal(started, 2)

  await mods.sync(() => cfg) // 幂等：不会重复 start
  assert.equal(started, 2)

  cfg = { modules: { a: true, b: false } }
  await mods.sync(() => cfg)
  assert.equal(mods.isRunning('a'), true)
  assert.equal(mods.isRunning('b'), false)

  cfg = { modules: { a: true, b: true } }
  await mods.sync(() => cfg)
  assert.equal(started, 3)
})

test('出错隔离：一个模块 factory 抛错，其它模块照常起，错误记在它自己头上', async () => {
  const errors = []
  const log = { ...silentLog, error: (...a) => errors.push(a.join(' ')) }
  const mods = createModules({ ctx: {}, log })
  mods.register({ id: 'bad', factory: () => { throw new Error('起不来') } })
  mods.register({ id: 'good', factory: () => ({ ok: true }) })

  await mods.sync(() => ({ modules: { bad: true, good: true } }))
  assert.equal(mods.isRunning('bad'), false)
  assert.equal(mods.isRunning('good'), true)
  const bad = mods.status().find((m) => m.id === 'bad')
  assert.match(bad.error, /起不来/)
  assert.ok(errors.length >= 1)
})

test('出错隔离：start 抛错也把已注册项撤掉，不留半截', async () => {
  const mods = createModules({ ctx: {}, log: silentLog })
  let disposed = false
  mods.register({
    id: 'half',
    factory({ scope }) {
      scope.add(() => {
        disposed = true
      })
      return {
        start() {
          throw new Error('start 失败')
        },
      }
    },
  })
  await mods.sync(() => ({ modules: { half: true } }))
  assert.equal(mods.isRunning('half'), false)
  assert.equal(disposed, true)
})

test('restart：先撤后起，最老的那批注册项一定被清掉', async () => {
  const mods = createModules({ ctx: {}, log: silentLog })
  const log = []
  mods.register({
    id: 'conn',
    factory({ scope }) {
      const n = log.length
      log.push(`new:${n}`)
      scope.add(() => log.push(`dispose:${n}`))
      return { start: () => log.push(`start:${n}`), stop: () => log.push(`stop:${n}`) }
    },
  })
  await mods.start('conn', () => ({}))
  await mods.restart('conn', () => ({}))
  assert.equal(log.length, 6, `顺序不对：${log.join(' → ')}`)
  assert.match(log[0], /^new:/)
  assert.match(log[1], /^start:/)
  assert.match(log[2], /^stop:/)
  assert.match(log[3], /^dispose:/, '旧实例的注册项必须在起新的之前撤掉')
  assert.match(log[4], /^new:/)
  assert.match(log[5], /^start:/)
})

test('dispose：全部停掉（定义留着，status 里能看到都停了）', async () => {
  const mods = createModules({ ctx: {}, log: silentLog })
  mods.register({ id: 'x', factory: () => ({}) })
  await mods.start('x', () => ({}))
  await mods.dispose()
  assert.deepEqual(mods.ids(), ['x'])
  assert.equal(mods.isRunning('x'), false)
  assert.equal(mods.service('x'), null)
  assert.deepEqual(mods.status(), [{ id: 'x', title: 'x', enabled: false, error: null }])
})

test('重复注册同一个 id 直接报错（配置写错就别默默覆盖）', () => {
  const mods = createModules({ ctx: {}, log: silentLog })
  mods.register({ id: 'dup', factory: () => ({}) })
  assert.throws(() => mods.register({ id: 'dup', factory: () => ({}) }), /重复注册/)
})
