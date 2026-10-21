<script lang="ts">
	import { onMount } from 'svelte';
	import { resolve } from '$app/paths';
	const base = (resolve as (path: string) => string)('/').replace(/\/$/, '');
	import Badge from '#lib/components/Badge.svelte';
	import Button from '#lib/components/Button.svelte';
	import Card from '#lib/components/Card.svelte';
	import Icon from '#lib/components/Icon.svelte';
	import Skeleton from '#lib/components/Skeleton.svelte';
	import StatCard from '#lib/components/StatCard.svelte';
	import { api } from '#lib/api';
	import type { IconName } from '#lib/icons';
	import { dateTime, relativeTime } from '#lib/format';
	import { toast } from '#lib/toast.svelte';
	import type { Migration, Stats } from '#lib/types';

	let stats = $state<Stats | null>(null);
	let migrations = $state<Migration[]>([]);
	let loading = $state(true);
	let refreshing = $state(false);

	async function load(silent = false) {
		if (!silent) loading = true;
		else refreshing = true;
		try {
			const [s, m] = await Promise.all([api.stats(), api.migrations()]);
			stats = s;
			migrations = m.items;
		} catch (err) {
			toast.fail(err, '无法读取服务状态');
		} finally {
			loading = false;
			refreshing = false;
		}
	}

	onMount(() => {
		void load();
	});

	let pendingMigrations = $derived(migrations.filter((m) => !m.applied).length);
</script>

<div class="space-y-5">
	<div class="flex flex-wrap items-center justify-between gap-3">
		<div>
			<p class="muted text-xs">
				{#if stats}
					数据库 {stats.dialect} · 版本 {stats.version}
				{:else}
					正在读取服务状态…
				{/if}
			</p>
		</div>
		<div class="flex items-center gap-2">
			<Button variant="secondary" size="sm" onclick={() => load(true)} disabled={refreshing}>
				<Icon name="refresh" size={14} class={refreshing ? 'animate-spin' : ''} />刷新
			</Button>
			<a href={`${base}/users/`} class="inline-flex">
				<Button size="sm"><Icon name="plus" size={14} />新建用户</Button>
			</a>
		</div>
	</div>

	{#if loading || !stats}
		<Skeleton rows={5} />
	{:else}
		<div class="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
			<StatCard label="账号" value={stats.users} icon="users" hint="EAS / IMAP / SMTP 登录名" />
			<StatCard label="邮件" value={stats.messages} icon="mail" hint="未删除的邮件总数" />
			<StatCard label="设备" value={stats.devices} icon="device" hint="已配对的 ActiveSync 客户端" />
			<StatCard
				label="发件队列"
				value={stats.outbox_pending + stats.outbox_failed}
				icon="activity"
				tone={stats.outbox_failed > 0 ? 'danger' : stats.outbox_pending > 0 ? 'warn' : 'ok'}
				hint={stats.outbox_failed > 0 ? `${stats.outbox_failed} 封已失败` : '全部待投递'}
			/>
		</div>

		<div class="grid gap-4 lg:grid-cols-3">
			<Card title="投递状态" subtitle="发信与队列" class="lg:col-span-2">
				<div class="space-y-3">
					<div class="flex items-center justify-between gap-3">
						<div class="flex items-center gap-2.5">
							<span
								class="flex h-8 w-8 items-center justify-center rounded-lg bg-zinc-100 text-zinc-500 dark:bg-zinc-800 dark:text-zinc-400"
							>
								<Icon name="mail" size={15} />
							</span>
							<div>
								<p class="text-sm font-medium">Cloudflare Email Sending</p>
								<p class="muted text-xs">外部地址通过 SMTP 465 隐式 TLS 投递</p>
							</div>
						</div>
						<Badge tone={stats.smtp_ready ? 'ok' : 'danger'}>
							{stats.smtp_ready ? '已配置' : '未配置'}
						</Badge>
					</div>
					<div class="flex items-center justify-between gap-3">
						<div class="flex items-center gap-2.5">
							<span
								class="flex h-8 w-8 items-center justify-center rounded-lg bg-zinc-100 text-zinc-500 dark:bg-zinc-800 dark:text-zinc-400"
							>
								<Icon name="globe" size={15} />
							</span>
							<div>
								<p class="text-sm font-medium">收信域名</p>
								<p class="muted text-xs">由 Cloudflare Email Routing 转发到 /api/inbound</p>
							</div>
						</div>
						<Badge tone={stats.domains > 0 ? 'ok' : 'warn'}>{stats.domains} 个</Badge>
					</div>
					{#if stats.outbox_failed > 0}
						<div
							class="flex items-center justify-between gap-3 rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-red-800 dark:border-red-900 dark:bg-red-950/40 dark:text-red-300"
						>
							<p class="flex items-center gap-2 text-xs">
								<Icon name="alert" size={14} />
								{stats.outbox_failed} 封邮件重试次数用尽
							</p>
							<a class="text-xs font-medium underline" href={`${base}/mail/`}>处理</a>
						</div>
					{/if}
				</div>
			</Card>

			<Card title="数据库" subtitle={`迁移 ${migrations.length} 个`}>
				<div class="space-y-2 text-xs">
					<div class="flex items-center justify-between">
						<span class="muted">引擎</span>
						<Badge tone="brand">{stats.dialect}</Badge>
					</div>
					<div class="flex items-center justify-between">
						<span class="muted">待应用迁移</span>
						<Badge tone={pendingMigrations > 0 ? 'danger' : 'ok'}>
							{pendingMigrations > 0 ? `${pendingMigrations} 个` : '已是最新'}
						</Badge>
					</div>
					{#if migrations.length > 0}
						<div class="border-t border-zinc-100 pt-2 dark:border-zinc-800">
							{#each migrations.slice(-3) as m (m.version)}
								<div class="flex items-center justify-between gap-2 py-0.5">
									<span class="muted font-mono">{String(m.version).padStart(4, '0')} {m.name}</span>
									<span class="muted shrink-0">{m.applied ? dateTime(m.applied_at).slice(0, 10) : '—'}</span>
								</div>
							{/each}
						</div>
					{/if}
					<a
						class="inline-flex items-center gap-1 pt-1 text-brand-600 hover:underline dark:text-brand-400"
						href={`${base}/diagnostics/`}
					>
						查看全部 <Icon name="arrowRight" size={12} />
					</a>
				</div>
			</Card>
		</div>

		<Card title="快捷入口" subtitle="常用运维操作">
			<div class="grid gap-2 sm:grid-cols-2 lg:grid-cols-4">
				{#each ([
					{ href: '/users/', icon: 'users', label: '管理账号', hint: '新建、改密、别名' },
					{ href: '/mail/', icon: 'mail', label: '处理队列', hint: '重试或丢弃' },
					{ href: '/calendars/', icon: 'calendar', label: '日历订阅', hint: '公开只读链接' },
					{ href: '/settings/', icon: 'settings', label: '发信设置', hint: 'SMTP 与证书' }
				] satisfies Array<{ href: string; icon: IconName; label: string; hint: string }>) as item (item.href)}
					<a
						href={`${base}${item.href}`}
						class="group flex items-center gap-3 rounded-lg border border-zinc-200 px-3 py-2.5 transition-colors hover:border-brand-300 hover:bg-brand-50/50 dark:border-zinc-800 dark:hover:border-brand-500/40 dark:hover:bg-brand-500/5"
					>
						<span
							class="flex h-8 w-8 items-center justify-center rounded-lg bg-zinc-100 text-zinc-500 group-hover:bg-white group-hover:text-brand-600 dark:bg-zinc-800 dark:text-zinc-400"
						>
							<Icon name={item.icon} size={15} />
						</span>
						<div class="min-w-0">
							<p class="text-[13px] font-medium">{item.label}</p>
							<p class="muted truncate text-[11px]">{item.hint}</p>
						</div>
					</a>
				{/each}
			</div>
		</Card>

		<p class="muted text-[11px]">状态读取于 {relativeTime(Math.floor(Date.now() / 1000))}</p>
	{/if}
</div>
