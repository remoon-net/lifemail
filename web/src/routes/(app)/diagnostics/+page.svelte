<script lang="ts">
	import { onMount } from 'svelte';
	import Badge from '$lib/components/Badge.svelte';
	import Card from '$lib/components/Card.svelte';
	import Icon from '$lib/components/Icon.svelte';
	import Skeleton from '$lib/components/Skeleton.svelte';
	import { api } from '$lib/api';
	import { dateTime } from '$lib/format';
	import { toast } from '$lib/toast.svelte';
	import type { Migration, Stats } from '$lib/types';

	let stats = $state<Stats | null>(null);
	let migrations = $state<Migration[]>([]);
	let dialect = $state('');
	let loading = $state(true);

	async function load() {
		loading = true;
		try {
			const [s, m] = await Promise.all([api.stats(), api.migrations()]);
			stats = s;
			migrations = m.items;
			dialect = m.dialect;
		} catch (err) {
			toast.fail(err, '无法读取诊断信息');
		} finally {
			loading = false;
		}
	}

	onMount(() => {
		void load();
	});

	const endpoints = [
		{ label: 'EAS', value: '/Microsoft-Server-ActiveSync', note: 'iOS / Outlook / Windows' },
		{ label: 'Autodiscover', value: '/autodiscover/autodiscover.xml', note: '自动发现' },
		{ label: '收信 API', value: 'POST /api/inbound', note: 'Cloudflare Worker 调用' },
		{ label: '日历订阅', value: '/calendar/{uuid}.ics', note: '只读 iCalendar' },
		{ label: '健康检查', value: '/healthz', note: '存活' },
		{ label: '就绪检查', value: '/readyz', note: '含数据库探活' },
		{ label: '指标', value: '/metrics', note: 'Prometheus，需 Admin Token' }
	];
</script>

<div class="space-y-4">
	{#if loading}
		<Skeleton rows={4} />
	{:else}
		<div class="grid gap-4 lg:grid-cols-3">
			<Card title="运行信息" class="lg:col-span-1">
				<dl class="space-y-2 text-xs">
					<div class="flex items-center justify-between gap-2">
						<dt class="muted">版本</dt>
						<dd class="font-mono">{stats?.version ?? 'dev'}</dd>
					</div>
					<div class="flex items-center justify-between gap-2">
						<dt class="muted">数据库</dt>
						<dd><Badge tone="brand">{dialect}</Badge></dd>
					</div>
					<div class="flex items-center justify-between gap-2">
						<dt class="muted">账号</dt>
						<dd class="tabular">{stats?.users}</dd>
					</div>
					<div class="flex items-center justify-between gap-2">
						<dt class="muted">邮件</dt>
						<dd class="tabular">{stats?.messages}</dd>
					</div>
					<div class="flex items-center justify-between gap-2">
						<dt class="muted">设备</dt>
						<dd class="tabular">{stats?.devices}</dd>
					</div>
				</dl>
			</Card>

			<Card title="迁移记录" subtitle={`${migrations.filter((m) => m.applied).length} / ${migrations.length} 已应用`} class="lg:col-span-2" padding="none">
				<div class="max-h-72 overflow-y-auto">
					<table class="w-full text-xs">
						<thead class="sticky top-0 bg-zinc-50 text-left text-[11px] text-zinc-500 dark:bg-zinc-900">
							<tr>
								<th class="px-4 py-2 font-medium">版本</th>
								<th class="px-4 py-2 font-medium">名称</th>
								<th class="px-4 py-2 font-medium">状态</th>
								<th class="px-4 py-2 font-medium">应用时间</th>
							</tr>
						</thead>
						<tbody>
							{#each migrations as m (m.version)}
								<tr class="border-t border-zinc-100 dark:border-zinc-800">
									<td class="px-4 py-2 font-mono">{String(m.version).padStart(4, '0')}</td>
									<td class="px-4 py-2">{m.name}</td>
									<td class="px-4 py-2">
										<Badge tone={m.applied ? 'ok' : 'danger'}>{m.applied ? '已应用' : '待应用'}</Badge>
									</td>
									<td class="muted px-4 py-2">{m.applied_at ? dateTime(m.applied_at) : '—'}</td>
								</tr>
							{/each}
						</tbody>
					</table>
				</div>
			</Card>
		</div>

		<Card title="服务端点" subtitle="客户端与运维使用">
			<ul class="divide-y divide-zinc-100 dark:divide-zinc-800">
				{#each endpoints as endpoint (endpoint.label)}
					<li class="flex flex-wrap items-center gap-x-3 gap-y-1 py-2 first:pt-0 last:pb-0">
						<span class="w-24 shrink-0 text-xs font-medium">{endpoint.label}</span>
						<code class="min-w-0 flex-1 truncate font-mono text-[11px] text-zinc-600 dark:text-zinc-300">
							{endpoint.value}
						</code>
						<span class="muted text-[11px]">{endpoint.note}</span>
					</li>
				{/each}
			</ul>
		</Card>

		<Card title="排查建议">
			<ol class="muted list-decimal space-y-1.5 pl-4 text-xs">
				<li>先看 <code class="font-mono">cmd=X status=… bytes=N</code>：N 很小说明服务端没返回内容，N 正常但客户端不认说明是结构问题。</li>
				<li>设置 <code class="font-mono">LIFEMAIL_EAS_DUMP</code> 可以把客户端请求原样落盘，便于离线复现。</li>
				<li>日历时间必须是 MS-ASDTIME 紧凑格式；附件属性要用 itemoperations 命名空间的 Range/Total/Data。</li>
				<li>响应头里的 <code class="font-mono">X-Request-Id</code> 可以直接在访问日志里检索整条链路。</li>
			</ol>
			<p class="muted mt-3 flex items-center gap-1 text-[11px]">
				<Icon name="external" size={12} />协议踩坑记录见仓库 <code class="font-mono">docs/eas-notes.md</code>
			</p>
		</Card>
	{/if}
</div>
