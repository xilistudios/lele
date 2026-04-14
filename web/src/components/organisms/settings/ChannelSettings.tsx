import {
  DingtalkChannelSettings,
  DiscordChannelSettings,
  FeishuChannelSettings,
  LineChannelSettings,
  MaixcamChannelSettings,
  NativeChannelSettings,
  OnebotChannelSettings,
  QqChannelSettings,
  SlackChannelSettings,
  TelegramChannelSettings,
  WhatsAppChannelSettings,
} from './channels'

export function ChannelSettings() {
  return (
    <div className="space-y-6">
      <NativeChannelSettings />
      <TelegramChannelSettings />
      <DiscordChannelSettings />
      <WhatsAppChannelSettings />
      <FeishuChannelSettings />
      <SlackChannelSettings />
      <LineChannelSettings />
      <OnebotChannelSettings />
      <MaixcamChannelSettings />
      <QqChannelSettings />
      <DingtalkChannelSettings />
    </div>
  )
}
