<script lang="ts">
	import { onMount } from 'svelte';
	import { resolve } from '$app/paths';
	const base = (resolve as (path: string) => string)('/').replace(/\/$/, '');
	import Badge from '#lib/components/Badge.svelte';
	import Button from '#lib/components/Button.svelte';
	import Card from '#lib/components/Card.svelte';
	import Field from '#lib/components/Field.svelte';
	import Icon from '#lib/components/Icon.svelte';
	import Skeleton from '#lib/components/Skeleton.svelte';
	import { api } from '#lib/api';
	import { toast } from '#lib/toast.svelte';
	import { input } from '#lib/ui';

	let host = $state('');
	let port = $state(465);
	let username = $state('');
	let from = $state('');
	let password = $state('');
	let passwordSet = $state(false);
	let clearPassword = $state(false);
	let loading = $state(true);
	let saving = $state(false);

	async function load() {
		loading = true;
		try {
			const settings = await api.settings();
			host = settings.smtp.host;
			port = settings.smtp.port;
			username = settings.smtp.username;
			from = settings.smtp.from;
			passwordSet = settings.smtp.password_set;
			password = '';
			clearPassword = false;
		} catch (err) {
			toast.fail(err, '无法读取设置');
		} finally {
			loading = false;
		}
	}

	onMount(() => {
		void load();
	});

	async function save() {
		const smtp: Record<string, unknown> = {
			host: host.trim(),
			port: Number(port),
			username: username.trim(),
			from: from.trim()
		};
		if (password) smtp.password = password;
		else if (clearPassword) smtp.password = '';

		saving = true;
		try {
			const settings = await api.saveSettings({ smtp });
			host = settings.smtp.host;
			port = settings.smtp.port;
			username = settings.smtp.username;
			from = settings.smtp.from;
			passwordSet = settings.smtp.password_set;
			password = '';
			clearPassword = false;
			toast.ok('发信设置已保存');
		} catch (err) {
			toast.fail(err, '保存失败');
		} finally {
			saving = false;
		}
	}
</script>

<div class="space-y-4">
	{#if loading}
		<Skeleton rows={4} />
	{:else}
		<div class="grid gap-4 lg:grid-cols-3">
			<Card title="发信（Cloudflare Email Sending）" subtitle="SMTP 465 隐式 TLS" class="lg:col-span-2">
				<form class="space-y-3" onsubmit={(e) => { e.preventDefault(); save(); }}>
					<div class="grid gap-3 sm:grid-cols-[1fr_120px]">
						<Field label="主机" for="smtp-host">
							<input id="smtp-host" class={input} bind:value={host} placeholder="smtp.mx.cloudflare.net" />
						</Field>
						<Field label="端口" for="smtp-port">
							<input id="smtp-port" class={input} type="number" min="1" max="65535" bind:value={port} />
						</Field>
					</div>
					<Field label="用户名" for="smtp-user" hint="Cloudflare Email Sending 固定为 api_token">
						<input id="smtp-user" class={input} bind:value={username} autocomplete="off" />
					</Field>
					<Field
						label="API Token"
						for="smtp-password"
						hint={passwordSet ? '已保存；留空表示保持不变' : '尚未配置，外部地址将无法投递'}
					>
						<input
							id="smtp-password"
							class={input}
							type="password"
							bind:value={password}
							placeholder={passwordSet ? '••••••••（保持不变）' : 'Cloudflare API Token'}
							autocomplete="new-password"
						/>
					</Field>
					<Field label="兜底 From" for="smtp-from" hint="用户地址不属于已配置域名时使用">
						<input id="smtp-from" class={input} type="email" bind:value={from} placeholder="noreply@example.com" />
					</Field>
					<label class="flex items-center gap-2 text-xs text-zinc-600 dark:text-zinc-400">
						<input type="checkbox" bind:checked={clearPassword} class="h-4 w-4 rounded border-zinc-300" />
						清除已保存的 Token
					</label>
					<div class="flex items-center justify-between gap-3 border-t border-zinc-100 pt-3 dark:border-zinc-800">
						<Badge tone={passwordSet ? 'ok' : 'danger'}>{passwordSet ? '投递就绪' : '未配置'}</Badge>
						<Button type="submit" disabled={saving}>{saving ? '保存中…' : '保存'}</Button>
					</div>
				</form>
			</Card>

			<div class="space-y-4">
				<Card title="域名与证书" subtitle="IMAPS / SMTPS 使用">
					<p class="muted text-xs">证书路径与收信域在单独的页面维护，保存后立即重新加载。</p>
					<a
						href={`${base}/domains/`}
						class="mt-3 inline-flex items-center gap-1 text-xs font-medium text-brand-600 hover:underline dark:text-brand-400"
					>
						打开域名设置 <Icon name="arrowRight" size={12} />
					</a>
				</Card>

				<Card title="部署提示">
					<ul class="muted space-y-2 text-xs">
						<li class="flex gap-2"><Icon name="check" size={14} class="mt-0.5 shrink-0 text-emerald-500" />反向代理必须透传 <code class="font-mono">X-Forwarded-For</code>，登录限流按真实 IP 计数。</li>
						<li class="flex gap-2"><Icon name="check" size={14} class="mt-0.5 shrink-0 text-emerald-500" />服务本身只监听 HTTP，TLS 由 CDN / 反向代理终止。</li>
						<li class="flex gap-2"><Icon name="check" size={14} class="mt-0.5 shrink-0 text-emerald-500" /><code class="font-mono">LIFEMAIL_PUBLIC_BASE_URL</code> 决定日历订阅链接是否为绝对地址。</li>
					</ul>
				</Card>
			</div>
		</div>
	{/if}
</div>
