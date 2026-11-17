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
import { createContext, useContext } from 'react'
import type { ReactNode } from 'react'

import type { Channel } from '../types'

/**
 * 渠道详情抽屉的打开入口。
 *
 * 列定义是 useMemo 产物，把回调塞进列闭包在表格里会拿到首次渲染的旧值（点击无反应），
 * 所以改由 context 下发，单元格里的 ChannelNameButton 每次渲染都取最新实现。
 */
type OpenChannelDetail = (channel: Channel) => void

const ChannelDetailContext = createContext<OpenChannelDetail | null>(null)

export function ChannelDetailProvider({
  onOpen,
  children,
}: {
  onOpen: OpenChannelDetail
  children: ReactNode
}) {
  return (
    <ChannelDetailContext.Provider value={onOpen}>
      {children}
    </ChannelDetailContext.Provider>
  )
}

export function useOpenChannelDetail(): OpenChannelDetail | null {
  return useContext(ChannelDetailContext)
}
