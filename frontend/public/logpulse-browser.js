/*!
 * LogPulse 浏览器错误采集脚本
 *
 * 自动捕获：未处理的 JS 异常、未处理的 Promise 拒绝、资源加载失败（script/img/css），
 * 可选捕获 console.error；批量通过 sendBeacon / fetch(keepalive) 上报到 LogPulse。
 *
 * 用法一（script 标签自动初始化）：
 *   <script src="https://logpulse.example.com/logpulse-browser.js"
 *           data-endpoint="https://logpulse.example.com/api/logs"
 *           data-service="web-frontend" data-release="1.4.2"></script>
 *
 * 用法二（手动初始化）：
 *   LogPulse.init({ endpoint: '/api/logs', service: 'web-frontend', captureConsole: true })
 *   LogPulse.captureError(err, { order_id: 42 })
 *   LogPulse.log('warn', 'checkout slow', { ms: 3200 })
 */
(function (global) {
  'use strict'
  if (global.LogPulse && global.LogPulse.__initialized) return

  var cfg = {
    endpoint: '/api/logs',
    service: 'web-frontend',
    release: '',
    captureConsole: false,
    sampleRate: 1, // 0~1，按比例采样
    maxPerMinute: 30, // 每分钟最多上报条数，防止错误风暴拖垮页面或后端
    flushInterval: 5000,
    maxBatch: 20,
    beforeSend: null, // function(event) { return event | null }，可用于脱敏或过滤
  }
  var queue = []
  var timer = null
  var sentThisMinute = 0
  var minuteStart = Date.now()
  var recent = {} // 去重：相同错误 10 秒内只报一次
  var origConsoleError = global.console && global.console.error

  function now() { return new Date().toISOString() }

  function allow(key) {
    var t = Date.now()
    if (t - minuteStart > 60000) { minuteStart = t; sentThisMinute = 0 }
    if (sentThisMinute >= cfg.maxPerMinute) return false
    if (recent[key] && t - recent[key] < 10000) return false
    if (Math.random() >= cfg.sampleRate) return false
    recent[key] = t
    sentThisMinute++
    return true
  }

  function errorMessage(err) {
    if (!err) return 'Unknown error'
    if (err instanceof Error) return (err.name || 'Error') + ': ' + err.message
    if (typeof err === 'string') return err
    try { return JSON.stringify(err) } catch (e) { return String(err) }
  }

  function enqueue(level, message, extra) {
    var event = {
      timestamp: now(),
      level: level,
      service: cfg.service,
      message: String(message).slice(0, 2000),
      url: global.location ? global.location.href : '',
      user_agent: global.navigator ? global.navigator.userAgent : '',
    }
    if (cfg.release) event.release = cfg.release
    for (var k in extra || {}) if (Object.prototype.hasOwnProperty.call(extra, k) && extra[k] !== undefined) event[k] = extra[k]
    if (typeof event.stack === 'string') event.stack = event.stack.slice(0, 8000)
    if (typeof cfg.beforeSend === 'function') {
      try { event = cfg.beforeSend(event) } catch (e) { /* 忽略 beforeSend 自身的错误 */ }
      if (!event) return
    }
    if (!allow(event.level + '|' + event.message + '|' + (event.source || ''))) return
    queue.push(event)
    if (queue.length >= cfg.maxBatch) flush()
    else if (!timer) timer = setTimeout(flush, cfg.flushInterval)
  }

  // 以 NDJSON（每行一个 JSON）、text/plain 发送：属于"简单请求"，跨域时无需预检
  function flush(useBeacon) {
    if (timer) { clearTimeout(timer); timer = null }
    if (!queue.length) return
    var body = queue.splice(0, queue.length).map(function (e) { return JSON.stringify(e) }).join('\n')
    try {
      if (useBeacon && global.navigator && global.navigator.sendBeacon) {
        if (global.navigator.sendBeacon(cfg.endpoint, body)) return
      }
      if (global.fetch) {
        global.fetch(cfg.endpoint, { method: 'POST', body: body, keepalive: true, headers: { 'Content-Type': 'text/plain' } })
          .catch(function () { /* 上报失败时静默丢弃，避免影响页面 */ })
      }
    } catch (e) { /* 静默 */ }
  }

  function onError(ev) {
    var target = ev.target
    // 资源加载失败（script/img/link）：事件不冒泡，需在捕获阶段监听
    if (target && target !== global && (target.src || target.href)) {
      enqueue('error', 'Resource load failed: <' + target.tagName.toLowerCase() + '> ' + (target.src || target.href), {
        resource: target.src || target.href,
      })
      return
    }
    var err = ev.error
    var source = ev.filename ? ev.filename + ':' + ev.lineno + ':' + ev.colno : undefined
    enqueue('error', 'Uncaught ' + (err ? errorMessage(err) : ev.message), {
      stack: err && err.stack,
      source: source,
    })
  }

  function onRejection(ev) {
    var reason = ev.reason
    enqueue('error', 'Unhandled Promise rejection: ' + errorMessage(reason), {
      stack: reason && reason.stack,
    })
  }

  var LogPulse = {
    __initialized: false,
    init: function (options) {
      for (var k in options || {}) if (Object.prototype.hasOwnProperty.call(options, k)) cfg[k] = options[k]
      if (LogPulse.__initialized) return LogPulse
      LogPulse.__initialized = true
      global.addEventListener('error', onError, true)
      global.addEventListener('unhandledrejection', onRejection)
      // 页面隐藏或关闭时用 sendBeacon 发送剩余数据
      global.addEventListener('visibilitychange', function () { if (global.document.visibilityState === 'hidden') flush(true) })
      global.addEventListener('pagehide', function () { flush(true) })
      if (cfg.captureConsole && origConsoleError) {
        global.console.error = function () {
          var args = Array.prototype.slice.call(arguments)
          var errArg = args.filter(function (a) { return a instanceof Error })[0]
          enqueue('error', args.map(errorMessage).join(' '), { stack: errArg && errArg.stack, source: 'console.error' })
          return origConsoleError.apply(global.console, args)
        }
      }
      return LogPulse
    },
    captureError: function (err, extra) {
      var data = { stack: err && err.stack }
      for (var k in extra || {}) data[k] = extra[k]
      enqueue('error', errorMessage(err), data)
    },
    log: function (level, message, extra) { enqueue(level || 'info', message, extra) },
    flush: function () { flush(false) },
  }
  global.LogPulse = LogPulse

  // script 标签上带 data-endpoint 时自动初始化
  var script = global.document && global.document.currentScript
  if (script && script.dataset && script.dataset.endpoint) {
    LogPulse.init({
      endpoint: script.dataset.endpoint,
      service: script.dataset.service || cfg.service,
      release: script.dataset.release || '',
      captureConsole: script.dataset.captureConsole === 'true',
    })
  }
})(typeof window !== 'undefined' ? window : this)
