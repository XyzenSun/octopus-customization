'use client';

import { useState } from "react"
import { motion } from "motion/react"
import { useTranslations } from 'next-intl'
import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { useLogin, useServerTime, fetchServerTime } from "@/api/endpoints/user"
import { useAPIKeyLogin } from "@/api/endpoints/apikey"
import Logo from "@/components/modules/logo"
import { KeyRound, User, ShieldCheck } from "lucide-react"
import {
  Tabs,
  TabsList,
  TabsHighlight,
  TabsHighlightItem,
  TabsTrigger,
  TabsContents,
  TabsContent,
} from "@/components/animate-ui/primitives/animate/tabs"

type LoginMode = 'user' | 'apikey';

export function LoginForm({ onLoginSuccess }: { onLoginSuccess?: () => void }) {
  const t = useTranslations('login')
  const [mode, setMode] = useState<LoginMode>('user')
  const [username, setUsername] = useState("")
  const [password, setPassword] = useState("")
  const [code, setCode] = useState("")
  const [apiKey, setApiKey] = useState("")
  const [error, setError] = useState<string | null>(null)

  const loginMutation = useLogin()
  const apiKeyLoginMutation = useAPIKeyLogin()
  // 只在用户名密码模式下轮询：API Key 登录与 TOTP 无关，没必要持续请求。
  const serverTimeQuery = useServerTime(mode === 'user')
  const twoFactorEnabled = serverTimeQuery.data?.two_factor_enabled ?? false
  // TOTP 固定为 6 位纯数字，本地先校验一次，避免明显不合法的验证码浪费一次请求。
  const isCodeValid = /^\d{6}$/.test(code)

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setError(null)

    if (mode === 'user' && twoFactorEnabled && !isCodeValid) {
      setError(t('twoFactor.invalid'))
      return
    }

    try {
      if (mode === 'user') {
        await loginMutation.mutateAsync({
          username,
          password,
          code: twoFactorEnabled ? code : undefined,
        })
      } else {
        await apiKeyLoginMutation.mutateAsync(apiKey)
      }

      onLoginSuccess?.()
    } catch (err: unknown) {
      // 账户登录：后端对密码错误和验证码错误返回同一个响应，前端同样不区分——
      // 区分开来会泄露"密码已经猜对了"，降低撞库难度。
      // 开启两步验证时附上服务器时间：TOTP 依赖绝对时间，设备与服务器相差超过
      // 30 秒就会算出不同的验证码，这是最常见且最难自查的失败原因。
      if (mode === 'user') {
        if (twoFactorEnabled) {
          try {
            const { server_time, timezone } = await fetchServerTime()
            setError(t('twoFactor.failedWithTime', { time: server_time, timezone }))
            return
          } catch {
            // 取时间失败不应掩盖登录失败本身，退回通用提示
          }
        }
        setError(t('error.generic'))
        return
      }
      // API Key 登录与撞库防护无关，保留后端的具体错误（已禁用、已过期等）
      setError(err instanceof Error ? err.message : t('error.generic'))
    }
  }

  const isPending = loginMutation.isPending || apiKeyLoginMutation.isPending

  const handleModeChange = (value: string) => {
    setMode(value as LoginMode)
    setError(null)
    setCode("")
  }

  return (
    <motion.div
      initial={{ opacity: 0 }}
      animate={{ opacity: 1 }}
      exit={{ opacity: 0 }}
      transition={{ duration: 0.3 }}
      className="min-h-screen flex items-center justify-center px-6 text-foreground"
    >
      <div className="w-full max-w-sm space-y-8">
        <header className="flex flex-col items-center gap-3">
          <Logo size={48} />
          <h1 className="text-2xl font-bold">Octopus</h1>
        </header>

        <Tabs value={mode} onValueChange={handleModeChange}>
          <TabsList className="flex p-1 bg-muted rounded-2xl">
            <TabsHighlight className="rounded-xl bg-background shadow-sm">
              <TabsHighlightItem value="user" className="flex-1">
                <TabsTrigger
                  value="user"
                  className="w-full flex items-center justify-center gap-2 py-2.5 px-4 rounded-xl text-sm font-medium transition-colors data-[state=active]:text-foreground data-[state=inactive]:text-muted-foreground data-[state=inactive]:hover:text-foreground"
                >
                  <User className="w-4 h-4" />
                  {t('mode.user')}
                </TabsTrigger>
              </TabsHighlightItem>
              <TabsHighlightItem value="apikey" className="flex-1">
                <TabsTrigger
                  value="apikey"
                  className="w-full flex items-center justify-center gap-2 py-2.5 px-4 rounded-xl text-sm font-medium transition-colors data-[state=active]:text-foreground data-[state=inactive]:text-muted-foreground data-[state=inactive]:hover:text-foreground"
                >
                  <KeyRound className="w-4 h-4" />
                  {t('mode.apikey')}
                </TabsTrigger>
              </TabsHighlightItem>
            </TabsHighlight>
          </TabsList>

          <form onSubmit={handleSubmit} className="space-y-6 pt-2">
            <TabsContents className="p-3 -mx-3 py-6">
              <TabsContent value="user" className="space-y-6">
                <Field>
                  <FieldLabel htmlFor="username">{t('username')}</FieldLabel>
                  <Input
                    id="username"
                    type="text"
                    placeholder={t('usernamePlaceholder')}
                    value={username}
                    onChange={(e) => setUsername(e.target.value)}
                    required={mode === 'user'}
                    disabled={isPending}
                  />
                </Field>
                <Field>
                  <FieldLabel htmlFor="password">{t('password')}</FieldLabel>
                  <Input
                    id="password"
                    type="password"
                    placeholder={t('passwordPlaceholder')}
                    value={password}
                    onChange={(e) => setPassword(e.target.value)}
                    required={mode === 'user'}
                    disabled={isPending}
                  />
                </Field>
                {twoFactorEnabled && (
                  <Field>
                    <FieldLabel htmlFor="code" className="flex items-center gap-2">
                      <ShieldCheck className="w-4 h-4" />
                      {t('twoFactor.label')}
                    </FieldLabel>
                    <Input
                      id="code"
                      type="text"
                      inputMode="numeric"
                      autoComplete="one-time-code"
                      maxLength={6}
                      placeholder={t('twoFactor.placeholder')}
                      value={code}
                      onChange={(e) => setCode(e.target.value.replace(/\D/g, ''))}
                      required
                      disabled={isPending}
                    />
                  </Field>
                )}
              </TabsContent>
              <TabsContent value="apikey">
                <Field>
                  <FieldLabel htmlFor="apikey">{t('apikey')}</FieldLabel>
                  <Input
                    id="apikey"
                    type="password"
                    placeholder={t('apikeyPlaceholder')}
                    value={apiKey}
                    onChange={(e) => setApiKey(e.target.value)}
                    required={mode === 'apikey'}
                    disabled={isPending}
                  />
                </Field>
              </TabsContent>
            </TabsContents>

            {error && <FieldDescription className="text-destructive">{error}</FieldDescription>}

            <Button
              type="submit"
              disabled={isPending || (mode === 'user' && twoFactorEnabled && !isCodeValid)}
              className="w-full"
            >
              {isPending ? t('button.loading') : t('button.submit')}
            </Button>
          </form>
        </Tabs>
      </div>
    </motion.div>
  )
}
