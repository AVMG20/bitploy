// The dashboard side of each notification channel type: its name, icon and
// settings form. A new type (Slack, email, ntfy) is a sender in
// internal/notify plus an entry here with its form.

import type { Component } from 'vue'
import { BellRing, MessagesSquare } from 'lucide-vue-next'
import { browserName, discordWebhookProblem, type PushState } from '@/lib/notifications'
import type { NotificationChannelConfig, NotificationChannelType } from '@/lib/types'
import BrowserConfigForm from './BrowserConfigForm.vue'
import DiscordConfigForm from './DiscordConfigForm.vue'

/** The props every config form takes (v-model is the config to send). */
export interface ChannelFormProps {
  /** The stored config, secrets masked, when editing. */
  existing?: NotificationChannelConfig
  error?: string
  idPrefix: string
  /** Browser notifications here: supported, allowed. */
  pushState: PushState
  /** The channel is the browser this page runs in. */
  current?: boolean
}

export interface ChannelTypeUI {
  type: NotificationChannelType
  label: string
  /** One line in the type picker. */
  blurb: string
  icon: Component
  form: Component
  /** The name a new channel starts with. */
  defaultName: () => string
  /** What is wrong with the config, or ''. editing: secrets may be left empty. */
  problem: (config: Record<string, unknown>, editing: boolean) => string
}

export const CHANNEL_TYPES: Record<NotificationChannelType, ChannelTypeUI> = {
  discord: {
    type: 'discord',
    label: 'Discord',
    blurb: 'Messages in a Discord channel, through a webhook.',
    icon: MessagesSquare,
    form: DiscordConfigForm,
    defaultName: () => 'Discord',
    problem: (c, editing) => {
      const url = String(c.webhook_url ?? '')
      if (editing && !url.trim()) return ''
      return discordWebhookProblem(url)
    },
  },
  browser: {
    type: 'browser',
    label: 'This browser',
    blurb: 'Notifications on this device, also with the tab closed.',
    icon: BellRing,
    form: BrowserConfigForm,
    defaultName: () => browserName(navigator.userAgent),
    problem: () => '',
  },
}
