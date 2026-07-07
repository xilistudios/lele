import { useCallback, useEffect, useState } from 'react'
import { useSettings } from '../../../contexts/SettingsContext'
import type { SafeClientInfo } from '../../../lib/types'
import { Modal, Spinner } from '../../atoms'
import { SettingsSection } from '../../molecules'

export function NativeClientsSettings() {
  const { api, t } = useSettings()
  const [clients, setClients] = useState<SafeClientInfo[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  // PIN pairing state
  const [deviceName, setDeviceName] = useState('')
  const [pinLoading, setPinLoading] = useState(false)
  const [pinInfo, setPinInfo] = useState<{ pin: string; expires: string } | null>(null)
  const [showPairModal, setShowPairModal] = useState(false)

  const fetchClients = useCallback(async () => {
    try {
      setLoading(true)
      const data = await api.listClients()
      setClients(data || [])
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to fetch native clients')
    } finally {
      setLoading(false)
    }
  }, [api])

  useEffect(() => {
    fetchClients()
  }, [fetchClients])

  const handleRemoveClient = async (clientId: string) => {
    if (!window.confirm(t('settings.native.confirmRevoke'))) return
    try {
      await api.removeClient(clientId)
      setClients((prev) => prev.filter((c) => c.client_id !== clientId))
    } catch (err) {
      alert(err instanceof Error ? err.message : 'Failed to revoke client')
    }
  }

  const handleGeneratePIN = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!deviceName.trim()) return
    try {
      setPinLoading(true)
      const resp = await api.getPIN(deviceName)
      setPinInfo(resp)
    } catch (err) {
      alert(err instanceof Error ? err.message : 'Failed to generate PIN')
    } finally {
      setPinLoading(false)
    }
  }

  const handleClosePairModal = () => {
    setShowPairModal(false)
    setPinInfo(null)
    setDeviceName('')
    fetchClients()
  }

  return (
    <div className="space-y-6">
      <SettingsSection
        title={t('settings.native.title')}
        description={t('settings.native.description')}
      >
        <div className="flex justify-between items-center mb-4">
          <h3 className="text-sm font-semibold text-text-primary">
            {t('settings.native.pairedClients')} ({clients.length})
          </h3>
          <button
            type="button"
            onClick={() => setShowPairModal(true)}
            className="rounded-md bg-accent-primary px-3 py-2 text-xs font-semibold text-text-on-accent shadow-sm hover:bg-accent-primary/90 transition-colors"
          >
            {t('settings.native.addClient')}
          </button>
        </div>

        {loading ? (
          <div className="flex h-32 items-center justify-center">
            <Spinner size="md" />
          </div>
        ) : error ? (
          <div className="rounded-md bg-state-error/10 p-4 text-sm text-state-error border border-state-error/20 animate-fade-in">
            {error}
          </div>
        ) : clients.length === 0 ? (
          <div className="rounded-md border border-dashed border-border p-8 text-center text-sm text-text-secondary bg-background-secondary/30">
            {t('settings.native.noClients')}
          </div>
        ) : (
          <div className="overflow-x-auto rounded-lg border border-border bg-background-secondary/20">
            <table className="w-full text-left border-collapse">
              <thead>
                <tr className="border-b border-border bg-background-secondary/50 text-xs font-medium text-text-secondary uppercase">
                  <th className="px-4 py-3">{t('settings.native.deviceName')}</th>
                  <th className="px-4 py-3">{t('settings.native.clientId')}</th>
                  <th className="px-4 py-3">{t('settings.native.lastSeen')}</th>
                  <th className="px-4 py-3">{t('settings.native.created')}</th>
                  <th className="px-4 py-3 text-right">{t('settings.native.actions')}</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border text-sm text-text-primary">
                {clients.map((client) => (
                  <tr
                    key={client.client_id}
                    className="hover:bg-background-secondary/30 transition-colors"
                  >
                    <td className="px-4 py-3 font-medium">
                      <div className="flex items-center gap-2">
                        <span
                          className="h-2 w-2 rounded-full bg-state-success animate-pulse"
                          title="Active"
                        />
                        {client.device_name}
                      </div>
                    </td>
                    <td className="px-4 py-3 text-xs font-mono text-text-secondary">
                      {client.client_id}
                    </td>
                    <td className="px-4 py-3 text-text-secondary">
                      {client.last_seen ? new Date(client.last_seen).toLocaleString() : '-'}
                    </td>
                    <td className="px-4 py-3 text-text-secondary">
                      {client.created ? new Date(client.created).toLocaleString() : '-'}
                    </td>
                    <td className="px-4 py-3 text-right">
                      <button
                        type="button"
                        onClick={() => handleRemoveClient(client.client_id)}
                        className="rounded bg-state-error/10 hover:bg-state-error/20 px-2 py-1 text-xs font-medium text-state-error transition-colors"
                      >
                        {t('settings.native.revoke')}
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </SettingsSection>

      <Modal
        isOpen={showPairModal}
        onClose={handleClosePairModal}
        title={t('settings.native.pairDeviceModalTitle')}
      >
        <div className="p-6 space-y-4">
          {!pinInfo ? (
            <form onSubmit={handleGeneratePIN} className="space-y-4">
              <p className="text-sm text-text-secondary">
                {t('settings.native.pairDeviceInstruction')}
              </p>
              <div>
                <label
                  htmlFor="deviceName"
                  className="block text-xs font-semibold text-text-secondary mb-1"
                >
                  {t('settings.native.deviceNameLabel')}
                </label>
                <input
                  type="text"
                  id="deviceName"
                  required
                  placeholder={t('settings.native.deviceNamePlaceholder')}
                  value={deviceName}
                  onChange={(e) => setDeviceName(e.target.value)}
                  className="w-full rounded-md border border-border bg-background-primary px-3 py-2 text-sm text-text-primary focus:border-accent-primary focus:outline-none transition-colors"
                />
              </div>
              <div className="flex justify-end gap-3 pt-2">
                <button
                  type="button"
                  onClick={handleClosePairModal}
                  className="rounded-md border border-border px-4 py-2 text-sm font-medium text-text-primary hover:bg-background-secondary transition-colors"
                >
                  {t('common.cancel')}
                </button>
                <button
                  type="submit"
                  disabled={pinLoading || !deviceName.trim()}
                  className="rounded-md bg-accent-primary px-4 py-2 text-sm font-medium text-text-on-accent hover:bg-accent-primary/90 disabled:opacity-50 transition-colors min-w-[80px]"
                >
                  {pinLoading ? <Spinner size="sm" /> : t('settings.native.generatePin')}
                </button>
              </div>
            </form>
          ) : (
            <div className="space-y-6 text-center py-4">
              <p className="text-sm text-text-secondary">
                {t('settings.native.pinGeneratedInstruction', { device: deviceName })}
              </p>

              <div className="bg-background-secondary rounded-lg p-6 border border-border max-w-xs mx-auto">
                <div className="text-4xl font-mono font-bold tracking-widest text-accent-primary animate-pulse">
                  {pinInfo.pin}
                </div>
                <div className="text-xs text-text-secondary mt-2">
                  {t('settings.native.expiresAt', {
                    time: new Date(pinInfo.expires).toLocaleTimeString(),
                  })}
                </div>
              </div>

              <div className="flex justify-center pt-2">
                <button
                  type="button"
                  onClick={handleClosePairModal}
                  className="rounded-md bg-accent-primary px-6 py-2 text-sm font-medium text-text-on-accent hover:bg-accent-primary/90 transition-colors"
                >
                  {t('common.close')}
                </button>
              </div>
            </div>
          )}
        </div>
      </Modal>
    </div>
  )
}
