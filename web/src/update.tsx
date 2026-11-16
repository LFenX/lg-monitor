import { Row } from './settings-rows'

/* The version row in settings. A development build says so instead of pretending to be a release. */
export function UpdateRow({ version }: { version: string }) {
  const dev = !version || version === 'dev'
  return <Row title="版本" hint={dev ? '开发版，不检查更新' : 'lgmon ' + version} />
}
