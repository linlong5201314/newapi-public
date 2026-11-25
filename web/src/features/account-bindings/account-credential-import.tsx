import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { getServerErrorMessage } from '@/lib/server-error-message'

import {
  importAccountCredentials,
  type NativeAuthorizationResult,
  type ProviderAuthorizationOptions,
} from './provider-authorization-api'

type Props = {
  provider: 'trae-cn' | 'raccoon'
  options?: ProviderAuthorizationOptions
  disabled?: boolean
  busy?: boolean
  onBusyChange?: (busy: boolean) => void
  onComplete: (result: NativeAuthorizationResult) => void
}

type AccountRecord = { id: string; label: string; value: Record<string, unknown> }

function stringField(value: unknown): string {
  return typeof value === 'string' || typeof value === 'number' ? String(value) : ''
}

function expiryField(value: unknown): number {
  const parsed = Number(value)
  if (!Number.isFinite(parsed)) return 0
  return Math.floor(parsed > 1e12 ? parsed / 1000 : parsed)
}

function asObject(value: unknown): Record<string, unknown> | null {
  return value && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : null
}

function toCredential(
  provider: Props['provider'],
  id: string,
  raw: Record<string, unknown>
): AccountRecord | null {
  const access = stringField(raw.access ?? raw.accessToken ?? raw.access_token ?? raw.token)
  const refresh = stringField(raw.refresh ?? raw.refreshToken ?? raw.refresh_token)
  if (!access || !refresh) return null
  const label = stringField(raw.accountId ?? raw.name ?? raw.uid ?? id)
  if (provider === 'raccoon') {
    return {
      id,
      label,
      value: {
        accessToken: access,
        refreshToken: refresh,
      },
    }
  }
  const uid = stringField(raw.uid ?? raw.userId ?? raw.user_id)
  const clientId = stringField(raw.clientId ?? raw.ClientID)
  if (!uid || (clientId && clientId !== 'ono9krqynydwx5')) return null
  return {
    id,
    label,
    value: {
      accessToken: access,
      refreshToken: refresh,
      uid,
      machineId: stringField(raw.machineId ?? raw.machine_id),
      deviceId: stringField(raw.deviceId ?? raw.device_id),
      expiresAt: expiryField(raw.expires ?? raw.expiresAt ?? raw.expires_at),
      devicePublicKey: stringField(raw.devicePublicKey),
      devicePrivateKey: stringField(raw.devicePrivateKey),
    },
  }
}

export function parseSingleAccountImport(
  provider: Props['provider'],
  input: unknown
): AccountRecord[] {
  const root = asObject(input)
  if (!root) return []
  // A Magpie library is selectable, never imported wholesale. Exactly one listed
  // account is submitted after the user chooses it.
  if (root['plugin-auth.json']) {
    return parseSingleAccountImport(provider, root['plugin-auth.json'])
  }
  const direct = toCredential(provider, provider, root)
  if (direct) return [direct]
  const nested = ['credentials', 'credential', 'account', 'auth']
    .map((key) => asObject(root[key]))
    .filter((item): item is Record<string, unknown> => item !== null)
  for (const item of nested) {
    const found = toCredential(provider, provider, item)
    if (found) return [found]
  }
  const accounts: AccountRecord[] = []
  for (const [id, value] of Object.entries(root)) {
    const object = asObject(value)
    if (!object) continue
    const isProvider =
      provider === 'raccoon' ? id === 'raccoon' : id === 'trae-cn' || id.startsWith('trae-cn#')
    if (!isProvider) continue
    const account = toCredential(provider, id, object)
    if (account) accounts.push(account)
  }
  return accounts
}

export function AccountCredentialImport({
  provider,
  options,
  disabled,
  busy: parentBusy,
  onBusyChange,
  onComplete,
}: Props) {
  const { t } = useTranslation()
  const [accounts, setAccounts] = useState<AccountRecord[]>([])
  const [selected, setSelected] = useState('')
  const [problem, setProblem] = useState('')
  const [busy, setBusy] = useState(false)
  const fileGeneration = useRef(0)
  const submitGeneration = useRef(0)
  const mounted = useRef(true)
  useEffect(() => {
    onBusyChange?.(busy)
  }, [busy, onBusyChange])
  useEffect(() => {
    fileGeneration.current++
    submitGeneration.current++
    setAccounts([])
    setSelected('')
    setProblem('')
    setBusy(false)
  }, [provider])
  useEffect(() => {
    const file = fileGeneration
    const submission = submitGeneration
    return () => {
      mounted.current = false
      file.current++
      submission.current++
    }
  }, [])
  const chooseFile = async (file?: File) => {
    const generation = ++fileGeneration.current
    submitGeneration.current++
    setBusy(false)
    setProblem('')
    setAccounts([])
    setSelected('')
    if (!file) return
    if (file.size > 4 * 1024 * 1024) {
      setProblem(t('Choose a JSON credential file smaller than 4 MB'))
      return
    }
    try {
      const parsed = parseSingleAccountImport(provider, JSON.parse(await file.text()))
      if (generation !== fileGeneration.current) return
      if (!parsed.length) throw new Error(t('No compatible account credential was found'))
      setAccounts(parsed)
      setSelected(parsed.length === 1 ? parsed[0].id : '')
    } catch (error) {
      setProblem(getServerErrorMessage(error, t('Invalid credential JSON file')))
    }
  }
  const submit = async () => {
    const account = accounts.find((item) => item.id === selected)
    if (!account || accounts.length !== 1 && !selected) return
    const generation = ++submitGeneration.current
    setBusy(true)
    setProblem('')
    try {
      const result = await importAccountCredentials(provider, account.value, options ?? {})
      if (generation !== submitGeneration.current || !mounted.current) return
      onComplete(result)
    } catch (error) {
      if (generation !== submitGeneration.current || !mounted.current) return
      setProblem(getServerErrorMessage(error, t('Failed to import account credentials')))
    } finally {
      if (generation === submitGeneration.current && mounted.current) {
        setBusy(false)
      }
    }
  }
  const id = `credential-import-${provider}`
  return (
    <div className='space-y-2 rounded-lg border border-dashed p-3'>
      <p className='text-muted-foreground text-xs'>
        {t(
          'Import one existing desktop or Magpie account. A Magpie library shows an account selector; only the selected credential is uploaded. Provider validation still applies.'
        )}
      </p>
      <Label htmlFor={id}>{t('Local authorization JSON or Magpie plugin-auth.json')}</Label>
      <Input
        id={id}
        type='file'
        accept='.json,application/json'
        disabled={disabled || busy || parentBusy}
        onChange={(event) => void chooseFile(event.target.files?.[0])}
      />
      {accounts.length > 1 && (
        <>
          <Label htmlFor={`${id}-account`}>{t('Choose exactly one account to import')}</Label>
          <select
            id={`${id}-account`}
            className='bg-background w-full rounded-md border px-3 py-2 text-sm'
            value={selected}
            disabled={disabled || busy || parentBusy}
            onChange={(event) => setSelected(event.target.value)}
          >
            <option value=''>{t('Select an account')}</option>
            {accounts.map((account) => (
              <option key={account.id} value={account.id}>
                {account.label} · {account.id}
              </option>
            ))}
          </select>
        </>
      )}
      {accounts.length === 1 && <p className='text-xs'>{accounts[0].label}</p>}
      <Button
        type='button'
        variant='outline'
        disabled={disabled || busy || parentBusy || !selected}
        onClick={() => void submit()}
      >
        {busy ? t('Validating account...') : t('Import selected account and create channel')}
      </Button>
      {problem && <p role='alert' className='text-destructive text-xs'>{problem}</p>}
    </div>
  )
}
