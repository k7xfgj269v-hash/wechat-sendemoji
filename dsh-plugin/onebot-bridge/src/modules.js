// 迷你模块宿主（照 au_rabot 的 src/core/modules.js 的意思，但只留够用的部分）。
//
// 一个模块 = { id, title, factory }。factory 拿到 { ctx, getConfig, log, bus, scope, modules }，
// 返回一个实例（可以有 start()/stop()，其余字段就是这个模块对外暴露的服务）。
//
// 三条规矩（au_rabot 也是这三条）：
//   1. 模块注册的一切（WS、HTTP、路由、事件监听、定时器、工具）都走它自己的 scope，
//      停用/卸载时一起撤掉 —— 不留野 socket、不留野 listener。
//   2. 出错隔离：某个模块 start/stop 抛错只记在它自己头上，其它模块照跑。
//   3. 开关现读配置：cfg.modules[id] === false 就关（缺省开），改配置下次 sync 生效。

/** 作用域：收一堆 disposer，dispose 时倒序执行。 */
export function createScope(label = '', log) {
  const offs = []
  return {
    get size() {
      return offs.length
    },
    /** 收一个 disposer（不是函数的忽略），返回原值方便串联。 */
    add(off) {
      if (typeof off === 'function') offs.push(off)
      return off
    },
    dispose() {
      while (offs.length) {
        const off = offs.pop()
        try {
          off()
        } catch (error) {
          log?.debug?.(`模块 ${label} 的收尾出错：`, error?.message || error)
        }
      }
    },
  }
}

/** 极简事件总线：模块之间只通过它说话。监听器自己负责在 scope 里登记。 */
export function createBus({ log } = {}) {
  const listeners = new Map()
  return {
    on(name, fn) {
      if (typeof fn !== 'function') return () => {}
      const list = listeners.get(name) || []
      list.push(fn)
      listeners.set(name, list)
      return () => {
        const cur = listeners.get(name) || []
        const i = cur.indexOf(fn)
        if (i >= 0) cur.splice(i, 1)
      }
    },
    emit(name, ...args) {
      for (const fn of [...(listeners.get(name) || [])]) {
        try {
          fn(...args)
        } catch (error) {
          log?.warn?.(`事件 ${name} 的监听器出错（其它监听器继续）：`, error?.message || error)
        }
      }
    },
    count(name) {
      return (listeners.get(name) || []).length
    },
  }
}

/**
 * @param {object} o
 * @param {object} o.ctx DSH 宿主 ctx
 * @param {{info:Function,warn:Function,error:Function,debug:Function}} o.log 日志器
 */
export function createModules({ ctx, log }) {
  const bus = createBus({ log })
  const defs = new Map()

  const api = {
    bus,
    /** 注册一个模块定义（不启动）。 */
    register(def) {
      if (!def || !def.id) throw new Error('模块必须带 id')
      if (defs.has(def.id)) throw new Error(`模块 ${def.id} 重复注册`)
      defs.set(def.id, { title: def.title || def.id, factory: def.factory, running: false, instance: null, scope: null, error: null })
      return api
    },
    /** 运行中的模块实例（没开就 null）。 */
    service(id) {
      const d = defs.get(id)
      return d && d.running ? d.instance : null
    },
    has(id) {
      return defs.has(id)
    },
    ids() {
      return [...defs.keys()]
    },
    isRunning(id) {
      return Boolean(defs.get(id)?.running)
    },
    status() {
      return [...defs.entries()].map(([id, d]) => ({ id, title: d.title, enabled: d.running, error: d.error }))
    },
    /** 把事件总线暴露给宿主之外的东西（测试用）。 */
    emit: (name, ...args) => bus.emit(name, ...args),
    async start(id, getConfig) {
      const d = defs.get(id)
      if (!d) throw new Error(`没有模块 ${id}`)
      if (d.running) return d.instance
      const scope = createScope(id, log)
      try {
        const instance = await d.factory({ ctx, getConfig, log, bus, scope, modules: api })
        d.instance = instance && typeof instance === 'object' ? instance : {}
        d.scope = scope
        if (typeof d.instance.start === 'function') await d.instance.start()
        d.running = true
        d.error = null
        log.info(`模块 ${id}（${d.title}）已启动`)
      } catch (error) {
        // 起不来就把这个模块已经注册的东西撤干净，别留半截
        try {
          scope.dispose()
        } catch {
          // 收尾失败也不影响别的模块
        }
        d.running = false
        d.instance = null
        d.scope = null
        d.error = String(error?.message || error)
        log.error(`模块 ${id} 启动失败（其它模块继续）：`, d.error)
      }
      return d.instance
    },
    async stop(id) {
      const d = defs.get(id)
      if (!d) return
      const had = d.running || d.instance
      try {
        if (typeof d.instance?.stop === 'function') await d.instance.stop()
      } catch (error) {
        log.warn(`模块 ${id} 停止时出错（继续撤注册）：`, error?.message || error)
      }
      try {
        d.scope?.dispose()
      } catch (error) {
        log.warn(`模块 ${id} 的注册项撤销时出错：`, error?.message || error)
      }
      d.running = false
      d.instance = null
      d.scope = null
      d.error = null
      if (had) log.info(`模块 ${id} 已停止（它注册的东西都撤了）`)
    },
    async restart(id, getConfig) {
      await api.stop(id)
      return api.start(id, getConfig)
    },
    /** 按配置启停（幂等）。 */
    async sync(getConfig) {
      const cfg = getConfig()
      const wanted = cfg?.modules || {}
      for (const [id, d] of defs) {
        const on = wanted[id] !== false
        if (on && !d.running) await api.start(id, getConfig)
        else if (!on && (d.running || d.instance)) await api.stop(id)
      }
      return api.status()
    },
    /** 全部停掉（总开关关、卸载、热重载收尾）。 */
    async stopAll() {
      for (const id of [...defs.keys()].reverse()) await api.stop(id)
    },
    /** 收尾：全部停掉。定义保留（status 里看得到「都停了」，排查 HMR 时有用）。 */
    async dispose() {
      await api.stopAll()
    },
  }

  return api
}
