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
import {
  AppWindow,
  BookOpen,
  Box,
  Cloud,
  Code2,
  Database,
  ExternalLink,
  FileText,
  Globe,
  Link2,
  Mail,
  MessageSquare,
  Music,
  Rocket,
  Rss,
  ShoppingCart,
  Star,
  Video,
  Zap,
  type LucideIcon,
} from 'lucide-react'

/**
 * [M4-FE] ExtLink icon resolver.
 *
 * The admin stores a short icon name (or an image URL / emoji). Names map to a
 * fixed lucide set; unknown names fall back to Globe. Values starting with
 * http(s):// or / render as an <img>; single grapheme clusters render as text
 * (emoji-friendly).
 */
const ICON_MAP: Record<string, LucideIcon> = {
  app: AppWindow,
  book: BookOpen,
  box: Box,
  cloud: Cloud,
  code: Code2,
  database: Database,
  docs: FileText,
  external: ExternalLink,
  globe: Globe,
  link: Link2,
  mail: Mail,
  chat: MessageSquare,
  music: Music,
  rocket: Rocket,
  rss: Rss,
  shop: ShoppingCart,
  star: Star,
  video: Video,
  zap: Zap,
}

type ExtLinkIconProps = {
  icon?: string
  name?: string
  className?: string
}

export function ExtLinkIcon({ icon, name, className }: ExtLinkIconProps) {
  const value = (icon ?? '').trim()
  if (/^(https?:)?\/\//.test(value) || value.startsWith('/')) {
    return (
      <img
        src={value}
        alt={name ?? ''}
        className={className}
        loading='lazy'
        referrerPolicy='no-referrer'
      />
    )
  }
  const Icon = ICON_MAP[value.toLowerCase()]
  if (Icon) {
    return <Icon className={className} aria-hidden />
  }
  if (value && [...value].length <= 2) {
    // Emoji / single glyph.
    return (
      <span className={className} role='img' aria-label={name ?? ''}>
        {value}
      </span>
    )
  }
  return <Globe className={className} aria-hidden />
}
