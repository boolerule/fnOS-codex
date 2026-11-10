import { beforeEach, describe, expect, it } from 'vitest'
import { setLocale, uiErrText, uiText, useI18n } from '@/composables/useI18n'

// 只测「前端自己可控」的部分：界面文案、参数替换、后端 code 的回退。
// 后端 uimsg_test.go 负责保证 code 在 zh/en 两份字典里都存在。
describe('i18n', () => {
  beforeEach(() => {
    localStorage.clear()
    setLocale('zh')
  })

  it('translates UI keys and switches locale', () => {
    const { t } = useI18n()
    expect(t('nav_overview')).toBe('概览')
    setLocale('en')
    expect(t('nav_overview')).toBe('Overview')
  })

  it('keeps workspace paths literal in a translated confirmation', () => {
    expect(useI18n().t('end_session_hint', { path: '/data/$&' })).toContain('/data/$&')
  })

  it('falls back to the backend original text when code is unknown', () => {
    expect(uiText('后端原文', { code: 'err_not_in_frontend' })).toBe('后端原文')
    expect(uiText('', undefined, 'fallback')).toBe('fallback')
  })

  it('renders ApiError-like objects through uiErrText', () => {
    expect(uiErrText({ code: 'not_installed', params: {} }, 'fallback')).toBe('尚未安装')
    expect(uiErrText(new Error('boom'), 'fallback')).toBe('boom')
  })
})
