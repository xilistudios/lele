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
  WebChannelSettings,
  WhatsAppChannelSettings,
} from './channels'

export function ChannelSettings() {
  return (
    <div className="space-y-6">
      <NativeChannelSettings />
      <WebChannelSettings />
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
