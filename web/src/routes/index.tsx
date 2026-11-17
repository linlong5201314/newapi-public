/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { useEffect } from 'react'

import { Home } from '@/features/home'
import { useAuthStore } from '@/stores/auth-store'

/**
 * 首页分流：已登录用户直接进 DeepSeek 风格对话页，未登录/未初始化时仍是
 * 原来的产品落地页（对外介绍、定价、排行榜都不动）。
 */
export const Route = createFileRoute('/')({
  component: RootPage,
})

function RootPage() {
  const user = useAuthStore((state) => state.auth.user)
  const navigate = useNavigate()

  useEffect(() => {
    if (!user) return
    void navigate({ to: '/demo', replace: true })
  }, [navigate, user])

  if (user) return null

  return <Home />
}
