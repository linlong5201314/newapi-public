import { Download } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'

export function AppDownload() {
  const { t } = useTranslation()
  return (
    <Button
      variant='outline'
      nativeButton={false}
      role='link'
      size='sm'
      className='shrink-0 gap-1.5 px-2 sm:px-3'
      render={<a href='/deepseek-newapi.apk' download='deepseek-newapi.apk' />}
      aria-label={t('Download App')}
      title={t('Download App')}
    >
      <Download className='size-4' aria-hidden='true' />
      <span>App</span>
    </Button>
  )
}
