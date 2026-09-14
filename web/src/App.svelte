<script>
  import { onMount, onDestroy } from 'svelte'

  const TYPES = ['gauge', 'counter', 'histogram', 'summary']
  const MODES = [
    { id: 'pushgateway', label: 'Pushgateway' },
    { id: 'remotewrite', label: 'Remote Write (/api/v1/write)' }
  ]

  let config = $state({ pushgatewayUrl: '…', remoteWriteUrl: '', remoteEnabled: false, sendMode: 'pushgateway', tokenPresent: false })
  let mode = $state('pushgateway')

  let job = $state('manual')
  let groupingLabels = $state([])

  let metricName = $state('')
  let metricHelp = $state('')
  let metricType = $state('gauge')
  let metricLabels = $state([])
  let value = $state('')
  let buckets = $state([{ le: '0.1', count: '0' }, { le: '1', count: '0' }, { le: '5', count: '0' }])
  let quantiles = $state([{ quantile: '0.5', value: '' }, { quantile: '0.9', value: '' }])
  let sum = $state('')
  let count = $state('')

  let busy = $state(false)
  let status = $state({ kind: '', message: '' })

  let repeat = $state(false)
  let intervalSec = $state(10)
  let durationSec = $state('')
  let running = $state(false)
  let sentCount = $state(0)
  let payload = null
  let timerId = null
  let stopAt = 0

  onMount(async () => {
    try {
      const res = await fetch('/api/config')
      if (res.ok) {
        config = await res.json()
        if (config.sendMode) mode = config.sendMode
      }
    } catch {
      /* ignore */
    }
  })

  function addLabel(list, name = '', val = '') {
    list.push({ name, value: val })
  }

  function removeLabel(list, index) {
    list.splice(index, 1)
  }

  function num(v) {
    const n = Number(v)
    return Number.isFinite(n) ? n : 0
  }

  function cleanLabels(list) {
    return list
      .filter((l) => l.name.trim() !== '')
      .map((l) => ({ name: l.name.trim(), value: l.value }))
  }

  function buildMetric() {
    const metric = {
      name: metricName.trim(),
      help: metricHelp.trim(),
      type: metricType,
      labels: cleanLabels(metricLabels)
    }

    if (metricType === 'gauge' || metricType === 'counter') {
      metric.value = num(value)
    }

    if (metricType === 'histogram') {
      metric.buckets = buckets
        .filter((b) => b.count !== '' && b.le.trim() !== '+Inf' && b.le.trim() !== '')
        .map((b) => ({
          le: num(b.le),
          count: Math.max(0, Math.trunc(num(b.count)))
        }))
      if (sum !== '') metric.sum = num(sum)
      if (count !== '') metric.count = Math.max(0, Math.trunc(num(count)))
    }

    if (metricType === 'summary') {
      metric.quantiles = quantiles
        .filter((q) => q.quantile !== '' && q.value !== '')
        .map((q) => ({ quantile: num(q.quantile), value: num(q.value) }))
      if (sum !== '') metric.sum = num(sum)
      if (count !== '') metric.count = Math.max(0, Math.trunc(num(count)))
    }

    return metric
  }

  function buildPayload() {
    return {
      mode,
      job: job.trim(),
      groupingLabels: cleanLabels(groupingLabels),
      metrics: [buildMetric()]
    }
  }

  async function pushOnce() {
    try {
      const res = await fetch('/api/push', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload)
      })
      const data = await res.json().catch(() => ({}))
      return res.ok
        ? { ok: true, message: data.message || 'Метрика отправлена' }
        : { ok: false, message: data.message || `Ошибка ${res.status}` }
    } catch (e) {
      return { ok: false, message: String(e) }
    }
  }

  async function submit() {
    status = { kind: '', message: '' }
    if (!metricName.trim()) {
      status = { kind: 'err', message: 'Укажите имя метрики' }
      return
    }
    if (repeat) {
      startLoop()
      return
    }

    payload = buildPayload()
    busy = true
    const res = await pushOnce()
    busy = false
    status = res.ok ? { kind: 'ok', message: res.message } : { kind: 'err', message: res.message }
  }

  async function tick() {
    if (!running) return
    const res = await pushOnce()
    if (!running) return
    sentCount++
    const iv = Math.max(1, Math.trunc(num(intervalSec)))
    if (!res.ok) {
      status = { kind: 'err', message: `${res.message} (отправлено: ${sentCount})` }
    } else if (stopAt) {
      const remain = Math.max(0, Math.round((stopAt - Date.now()) / 1000))
      status = { kind: 'ok', message: `Отправлено: ${sentCount}, осталось ~${remain}с` }
    } else {
      status = { kind: 'ok', message: `Отправлено: ${sentCount}, следующая через ${iv}с` }
    }

    if (stopAt && Date.now() >= stopAt) {
      stopLoop(false)
      status = { kind: 'ok', message: `Готово: ${sentCount} отправок` }
      return
    }
    timerId = setTimeout(tick, iv * 1000)
  }

  function startLoop() {
    payload = buildPayload()
    running = true
    sentCount = 0
    const dur = num(durationSec)
    stopAt = dur > 0 ? Date.now() + dur * 1000 : 0
    status = { kind: 'ok', message: stopAt ? `Цикл запущен до ${new Date(stopAt).toLocaleTimeString()}` : 'Цикл запущен (бесконечно)' }
    tick()
  }

  function stopLoop(manual = true) {
    running = false
    if (timerId) {
      clearTimeout(timerId)
      timerId = null
    }
    if (manual) status = { kind: 'ok', message: `Остановлено, отправок: ${sentCount}` }
  }

  onDestroy(() => {
    if (timerId) clearTimeout(timerId)
  })
</script>

<header>
  <h1>Metrics Generator</h1>
  <div class="meta">
    <span>Pushgateway: <code>{config.pushgatewayUrl}</code></span>
    {#if config.remoteEnabled}
      <span>Remote write: <code>{config.remoteWriteUrl}</code></span>
    {:else}
      <span>Remote write: не настроен</span>
    {/if}
    <span class="token" class:ok={config.tokenPresent} class:warn={!config.tokenPresent}>
      {config.tokenPresent ? 'SA token: есть' : 'SA token: не найден'}
    </span>
  </div>
</header>

<section class="panel">
  <h2>Отправка</h2>
  <label class="field">
    <span>Куда писать метрику</span>
    <select bind:value={mode}>
      {#each MODES as m}
        <option value={m.id} disabled={m.id === 'remotewrite' && !config.remoteEnabled}>
          {m.label}{m.id === 'remotewrite' && !config.remoteEnabled ? ' (не настроен)' : ''}
        </option>
      {/each}
    </select>
  </label>

  <label class="check">
    <input type="checkbox" bind:checked={repeat} />
    <span>Писать периодически (loop)</span>
  </label>
  {#if repeat}
    <div class="grid">
      <label class="field">
        <span>Интервал (сек)</span>
        <input bind:value={intervalSec} type="number" step="1" min="1" />
      </label>
      <label class="field">
        <span>Продолжительность (сек, пусто = бесконечно)</span>
        <input bind:value={durationSec} type="number" step="1" min="1" placeholder="∞" />
      </label>
    </div>
  {/if}

  <h2 class="sub">Job</h2>
  <label class="field">
    <span>Имя job</span>
    <input bind:value={job} placeholder="manual" />
  </label>

  <div class="labels-head">
    <span>Grouping labels</span>
    <button type="button" class="ghost" onclick={() => addLabel(groupingLabels)}>+ label</button>
  </div>
  {#if groupingLabels.length === 0}
    <p class="hint">
      Только для Pushgateway. Идентифицируют группу в pushgateway (например instance, env). Для Remote Write игнорируются.
    </p>
  {/if}
  {#each groupingLabels as label, i}
    <div class="row">
      <input bind:value={label.name} placeholder="name" />
      <input bind:value={label.value} placeholder="value" />
      <button type="button" class="ghost" onclick={() => removeLabel(groupingLabels, i)}>×</button>
    </div>
  {/each}
</section>

<section class="panel">
  <h2>Метрика</h2>
  <div class="grid">
    <label class="field">
      <span>Имя</span>
      <input bind:value={metricName} placeholder="my_metric_total" />
    </label>
    <label class="field">
      <span>Тип</span>
      <select bind:value={metricType}>
        {#each TYPES as t}
          <option value={t}>{t}</option>
        {/each}
      </select>
    </label>
  </div>

  <label class="field">
    <span>HELP</span>
    <input bind:value={metricHelp} placeholder="Описание метрики" />
  </label>

  <div class="labels-head">
    <span>Labels</span>
    <button type="button" class="ghost" onclick={() => addLabel(metricLabels)}>+ label</button>
  </div>
  {#each metricLabels as label, i}
    <div class="row">
      <input bind:value={label.name} placeholder="name" />
      <input bind:value={label.value} placeholder="value" />
      <button type="button" class="ghost" onclick={() => removeLabel(metricLabels, i)}>×</button>
    </div>
  {/each}

  {#if metricType === 'gauge' || metricType === 'counter'}
    <label class="field">
      <span>Значение</span>
      <input bind:value={value} type="number" step="any" placeholder="0" />
    </label>
  {/if}

  {#if metricType === 'histogram'}
    <div class="labels-head">
      <span>Бакеты (count — кумулятивный)</span>
      <button type="button" class="ghost" onclick={() => addLabel(buckets, '', '0')}>+ bucket</button>
    </div>
    {#each buckets as bucket, i}
      <div class="row">
        <input bind:value={bucket.le} placeholder="le (напр. 0.5 или +Inf)" />
        <input bind:value={bucket.count} type="number" step="1" min="0" placeholder="count" />
        <button type="button" class="ghost" onclick={() => removeLabel(buckets, i)}>×</button>
      </div>
    {/each}
  {/if}

  {#if metricType === 'summary'}
    <div class="labels-head">
      <span>Квантили</span>
      <button type="button" class="ghost" onclick={() => addLabel(quantiles, '', '')}>+ quantile</button>
    </div>
    {#each quantiles as q, i}
      <div class="row">
        <input bind:value={q.quantile} placeholder="quantile (0..1)" />
        <input bind:value={q.value} type="number" step="any" placeholder="value" />
        <button type="button" class="ghost" onclick={() => removeLabel(quantiles, i)}>×</button>
      </div>
    {/each}
  {/if}

  {#if metricType === 'histogram' || metricType === 'summary'}
    <div class="grid">
      <label class="field">
        <span>sum (необязательно)</span>
        <input bind:value={sum} type="number" step="any" placeholder="0" />
      </label>
      <label class="field">
        <span>count (необязательно)</span>
        <input bind:value={count} type="number" step="1" min="0" placeholder="0" />
      </label>
    </div>
  {/if}
</section>

<div class="actions">
  {#if running}
    <button class="primary danger" onclick={() => stopLoop()}>Стоп</button>
  {:else}
    <button class="primary" onclick={submit} disabled={busy}>
      {busy ? 'Отправка…' : repeat ? 'Запустить цикл' : 'Отправить'}
    </button>
  {/if}
  {#if status.kind}
    <span class="status" class:ok={status.kind === 'ok'} class:err={status.kind === 'err'}>
      {status.message}
    </span>
  {/if}
</div>

<style>
  header h1 {
    font-size: 22px;
    margin: 0 0 8px;
  }

  .meta {
    display: flex;
    flex-wrap: wrap;
    gap: 12px;
    color: var(--muted);
    font-size: 13px;
    margin-bottom: 20px;
  }

  .meta code {
    color: var(--text);
  }

  .token.ok {
    color: var(--ok);
  }

  .token.warn {
    color: #e0a33a;
  }

  .panel {
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 18px;
    margin-bottom: 18px;
  }

  h2 {
    font-size: 13px;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: var(--muted);
    margin: 0 0 14px;
  }

  h2.sub {
    margin-top: 18px;
  }

  .field {
    display: flex;
    flex-direction: column;
    gap: 6px;
    margin-bottom: 12px;
  }

  .field > span {
    font-size: 12px;
    color: var(--muted);
  }

  .grid {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 12px;
  }

  input,
  select {
    background: var(--panel-2);
    border: 1px solid var(--border);
    border-radius: 8px;
    color: var(--text);
    padding: 9px 11px;
    font-size: 14px;
    font-family: inherit;
    width: 100%;
  }

  input:focus,
  select:focus {
    outline: none;
    border-color: var(--accent);
  }

  .labels-head {
    display: flex;
    justify-content: space-between;
    align-items: center;
    margin: 6px 0 8px;
    font-size: 12px;
    color: var(--muted);
  }

  .row {
    display: grid;
    grid-template-columns: 1fr 1fr auto;
    gap: 8px;
    margin-bottom: 8px;
  }

  .hint {
    color: var(--muted);
    font-size: 12px;
    margin: 0 0 8px;
  }

  .check {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-bottom: 14px;
    cursor: pointer;
    user-select: none;
  }

  .check input {
    width: auto;
    accent-color: var(--accent);
  }

  button {
    cursor: pointer;
    font-family: inherit;
    font-size: 14px;
    border-radius: 8px;
    border: 1px solid var(--border);
  }

  .ghost {
    background: transparent;
    color: var(--muted);
    padding: 0 12px;
    min-width: 40px;
  }

  .ghost:hover {
    color: var(--text);
    border-color: var(--accent);
  }

  .actions {
    display: flex;
    align-items: center;
    gap: 14px;
    flex-wrap: wrap;
  }

  .primary {
    background: var(--accent);
    border-color: var(--accent);
    color: white;
    padding: 11px 20px;
    font-weight: 600;
  }

  .primary:hover:not(:disabled) {
    background: var(--accent-hover);
  }

  .primary:disabled {
    opacity: 0.6;
    cursor: not-allowed;
  }

  .primary.danger {
    background: var(--err);
    border-color: var(--err);
  }

  .primary.danger:hover:not(:disabled) {
    background: #ff7373;
  }

  .status.ok {
    color: var(--ok);
  }

  .status.err {
    color: var(--err);
  }
</style>
