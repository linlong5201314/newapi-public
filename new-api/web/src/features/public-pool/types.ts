export interface PublicOwner {
  user_id: number
  name: string
  avatar: string
}

export interface PublicChannelView {
  channel_id: number
  channel_name: string
  owner_name: string
  owner_avatar: string
  tier: string
  response_time: number
}

export interface PublicModelItem {
  model: string
  providers: number
  free_count: number
  fastest_ms: number
  rating: number
  comments: number
  owners: PublicOwner[]
  channels: PublicChannelView[]
}

export interface PublicComment {
  id: number
  model: string
  channel_id: number
  channel_name: string
  user_id: number
  username: string
  display_name: string
  avatar: string
  content: string
  rating: number
  created_at: number
}

export interface MyChannelRow {
  id: number
  name: string
  visibility?: number
  tier?: string
  models?: string
  status?: number
  allow_ips?: string
}
