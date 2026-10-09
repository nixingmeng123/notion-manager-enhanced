import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { triggerRefresh, updateSettings } from '../api'
import type { SearchSettings } from '../api'
import type { DashboardData } from '../types'

export function AgentPanel({ settings, data, onSaved, onRefresh }: {
  settings: SearchSettings
  data: DashboardData | null
  onSaved: (settings: SearchSettings) => void
  onRefresh: () => void
}) {
  const { t } = useTranslation()
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [timeout, setTimeoutValue] = useState(settings.agent_timeout_seconds)
  const [poll, setPoll] = useState(settings.agent_poll_interval_ms)
  const [showModels, setShowModels] = useState(false)
  useEffect(() => { setTimeoutValue(settings.agent_timeout_seconds); setPoll(settings.agent_poll_interval_ms) }, [settings])
  const save = async (values: Parameters<typeof updateSettings>[0]) => {
    setBusy(true)
    setError('')
    try { onSaved(await updateSettings(values)); onRefresh() }
    catch (e) { setError(e instanceof Error ? e.message : String(e)) }
    finally { setBusy(false) }
  }
  const refresh = async () => {
    setBusy(true)
    setError('')
    try { await triggerRefresh(); onRefresh() }
    catch (e) { setError(e instanceof Error ? e.message : String(e)) }
    finally { setBusy(false) }
  }
  const failures = Object.values(data?.agent_stats?.failures ?? {}).reduce((total, n) => total + n, 0)
  const models = data?.protocol_models?.filter(m => m.id.includes('/')) ?? []
  return (
    <section className="mb-6 px-4 py-3 bg-bg-card border border-white/5 rounded-lg text-[12px]">
      <div className="flex flex-wrap gap-4 items-center">
        <strong className="text-text-primary">{t('agent.title')}</strong>
        <label className="flex gap-2 items-center text-text-secondary">
          <input type="checkbox" checked={settings.agent_v3_enabled} disabled={busy}
            onChange={e => save({ agent_v3_enabled: e.target.checked, ...(!e.target.checked ? { default_protocol: 'workflow' as const } : {}) })} />
          {t('agent.enabled')}
        </label>
        <label className="flex gap-2 items-center text-text-secondary">
          {t('agent.default')}
          <select className="bg-bg-secondary rounded px-2 py-1 text-text-primary" value={settings.default_protocol} disabled={busy}
            onChange={e => save({ default_protocol: e.target.value as 'workflow' | 'agent' })}>
            <option value="workflow">Workflow</option>
            <option value="agent" disabled={!settings.agent_v3_enabled}>Agent V3</option>
          </select>
        </label>
        <label className="flex gap-2 items-center text-text-secondary">
          {t('agent.timeout')}
          <input aria-label={t('agent.timeout')} className="w-20 bg-bg-secondary rounded px-2 py-1 text-text-primary" type="number" min="1" max="1800" value={timeout}
            onChange={e => setTimeoutValue(Number(e.target.value))} onBlur={() => timeout !== settings.agent_timeout_seconds && save({ agent_timeout_seconds: timeout })} disabled={busy} />
        </label>
        <label className="flex gap-2 items-center text-text-secondary">
          {t('agent.poll')}
          <input aria-label={t('agent.poll')} className="w-20 bg-bg-secondary rounded px-2 py-1 text-text-primary" type="number" min="100" max="30000" value={poll}
            onChange={e => setPoll(Number(e.target.value))} onBlur={() => poll !== settings.agent_poll_interval_ms && save({ agent_poll_interval_ms: poll })} disabled={busy} />
        </label>
      </div>
      <p className="mt-3 text-text-muted">{t('agent.modes')}</p>
      <div className="flex flex-wrap gap-4 items-center mt-3 text-text-secondary">
        <span>{t('agent.requests')}: {data?.agent_stats?.requests ?? 0}</span>
        <span>{t('agent.failures')}: {failures}</span>
        <button className="text-notion-blue cursor-pointer" onClick={() => setShowModels(!showModels)}>{t('agent.models')} ({models.length}) {showModels ? '▴' : '▾'}</button>
        <button className="text-notion-blue cursor-pointer disabled:opacity-40" disabled={busy || !settings.agent_v3_enabled} onClick={refresh}>{t('agent.refresh')}</button>
      </div>
      {failures > 0 && <p className="mt-2 text-text-muted">{Object.entries(data?.agent_stats?.failures ?? {}).map(([kind, count]) => `${kind}: ${count}`).join(' · ')}</p>}
      {error && <p role="alert" className="mt-2 text-err">{error}</p>}
      {showModels && <div className="mt-3 overflow-x-auto max-h-80 overflow-y-auto">
        <p className="text-text-muted mb-2">{t('agent.catalog_note')}</p>
        <table className="w-full text-left text-[11px]">
          <thead className="text-text-muted"><tr>{['model', 'protocol', 'available', 'authorization', 'efforts', 'slug'].map(key => <th key={key} className="p-2 font-medium">{t(`agent.${key}`)}</th>)}</tr></thead>
          <tbody>{models.map(model => <tr key={model.id} className="border-t border-white/5 text-text-secondary">
            <td className="p-2 whitespace-nowrap text-text-primary">{model.id}</td><td className="p-2">{model.protocol}</td>
            <td className={`p-2 ${model.available ? 'text-ok' : 'text-text-muted'}`}>{t(model.available ? 'agent.yes' : 'agent.no')}</td>
            <td className="p-2">{model.authorization}</td><td className="p-2">{model.reasoning_efforts?.join(', ') || '—'}<br /><span className="text-text-muted">{model.reasoning_source}</span></td>
            <td className="p-2 font-mono whitespace-nowrap">{model.internal_slug}</td>
          </tr>)}</tbody>
        </table>
      </div>}
    </section>
  )
}
