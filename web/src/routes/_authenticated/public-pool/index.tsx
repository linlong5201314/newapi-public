import { createFileRoute, redirect } from '@tanstack/react-router'

import { PublicPoolPage } from '@/features/public-pool'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

export const Route = createFileRoute('/_authenticated/public-pool/')({
  beforeLoad: () => {
    const { auth } = useAuthStore.getState()
    if (!auth.user || auth.user.role < ROLE.USER) {
      throw redirect({ to: '/403' })
    }
  },
  component: PublicPoolPage,
})
