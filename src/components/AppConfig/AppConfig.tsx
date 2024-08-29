import { AppPluginMeta, PluginConfigPageProps, PluginMeta } from '@grafana/data';

export type AppPluginSettings = {};

type State = {};

export interface AppConfigProps extends PluginConfigPageProps<AppPluginMeta<AppPluginSettings>> {}

export const AppConfig = ({ plugin }: AppConfigProps) => {
  return;
};
