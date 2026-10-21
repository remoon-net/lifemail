<script lang="ts">
	import type { Snippet } from 'svelte';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	const base = (resolve as (path: string) => string)('/').replace(/\/$/, '');
	import { page } from '$app/state';
	import Icon from '#lib/components/Icon.svelte';
	import Skeleton from '#lib/components/Skeleton.svelte';
	import { session } from '#lib/session.svelte';
	import { theme } from '#lib/theme.svelte';
	import { iconButton } from '#lib/ui';
	import type { IconName } from '#lib/icons';

	let { children }: { children: Snippet } = $props();
	let mobileOpen = $state(false);

	const nav: Array<{ href: string; label: string; icon: IconName; group: string }> = [
		{ href: '/', label: '概览', icon: 'dashboard', group: '监控' },
		{ href: '/users/', label: '用户', icon: 'users', group: '账号' },
		{ href: '/aliases/', label: '地址与别名', icon: 'star', group: '账号' },
		{ href: '/domains/', label: '域名与证书', icon: 'globe', group: '投递' },
		{ href: '/mail/', label: '发件队列', icon: 'mail', group: '投递' },
		{ href: '/devices/', label: '设备', icon: 'device', group: '投递' },
		{ href: '/calendars/', label: '日历订阅', icon: 'calendar', group: '功能' },
		{ href: '/settings/', label: '设置', icon: 'settings', group: '系统' },
		{ href: '/diagnostics/', label: '诊断', icon: 'activity', group: '系统' }
	];

	const groups = ['监控', '账号', '投递', '功能', '系统'];

	function hrefFor(path: string): string {
		return path === '/' ? `${base}/` : `${base}${path}`;
	}

	function isActive(path: string): boolean {
		const current = page.url.pathname;
		const target = hrefFor(path);
		if (path === '/') return current === `${base}/` || current === base;
		return current === target || current.startsWith(target);
	}

	const titles: Record<string, { title: string; subtitle: string }> = {
		'/': { title: '概览', subtitle: '服务状态与关键指标' },
		'/users': { title: '用户', subtitle: 'EAS · IMAPS · SMTPS 登录账号' },
		'/aliases': { title: '地址与别名', subtitle: '每个账号可收信的地址' },
		'/domains': { title: '域名与证书', subtitle: '收信域与邮件协议 TLS 证书' },
		'/mail': { title: '发件队列', subtitle: '待投递与投递失败的邮件' },
		'/devices': { title: '设备', subtitle: '已连接的 ActiveSync 客户端' },
		'/calendars': { title: '日历订阅', subtitle: '只读 iCalendar 公开链接' },
		'/settings': { title: '设置', subtitle: '发信参数与运行时配置' },
		'/diagnostics': { title: '诊断', subtitle: '版本、数据库与端点' }
	};

	let current = $derived.by(() => {
		const path = page.url.pathname.replace(base, '').replace(/\/+$/, '');
		return titles[path === '' ? '/' : path] ?? titles['/'];
	});

	$effect(() => {
		if (session.ready && !session.authenticated) {
			void goto(`${base}/login/`);
		}
	});

	$effect(() => {
		// Close the drawer after navigating on mobile.
		void page.url.pathname;
		mobileOpen = false;
	});

	async function logout() {
		await session.logout();
		await goto(`${base}/login/`);
	}
</script>

<svelte:head>
	<title>{current.title} · lifemail</title>
</svelte:head>

{#if !session.ready}
	<div class="mx-auto max-w-5xl px-6 py-16">
		<Skeleton rows={6} />
	</div>
{:else if session.authenticated}
	<div class="min-h-dvh lg:grid lg:grid-cols-[15rem_1fr]">
		{#if mobileOpen}
			<button
				type="button"
				class="fixed inset-0 z-30 bg-zinc-950/40 backdrop-blur-xs lg:hidden"
				onclick={() => (mobileOpen = false)}
				aria-label="关闭菜单"
			></button>
		{/if}

		<aside
			class="fixed inset-y-0 left-0 z-40 flex w-60 flex-col border-r border-zinc-200 bg-white transition-transform dark:border-zinc-800 dark:bg-zinc-900 lg:sticky lg:top-0 lg:z-auto lg:h-dvh lg:w-auto lg:translate-x-0 {mobileOpen
				? 'translate-x-0'
				: '-translate-x-full'}"
		>
			<div class="flex h-14 items-center gap-2.5 border-b border-zinc-200 px-4 dark:border-zinc-800">
				<span
					class="flex h-8 w-8 items-center justify-center rounded-lg bg-brand-600 text-sm font-semibold text-white"
				>
					L
				</span>
				<div class="min-w-0">
					<p class="truncate text-sm font-semibold tracking-tight">lifemail</p>
					<p class="muted truncate text-[11px]">{session.version}</p>
				</div>
				<button
					type="button"
					class="{iconButton} ml-auto lg:hidden"
					onclick={() => (mobileOpen = false)}
					aria-label="关闭菜单"
				>
					<Icon name="x" size={16} />
				</button>
			</div>

			<nav class="flex-1 space-y-4 overflow-y-auto px-3 py-4">
				{#each groups as group (group)}
					<div>
						<p class="px-2 pb-1 text-[10px] font-semibold uppercase tracking-widest text-zinc-400 dark:text-zinc-500">
							{group}
						</p>
						<ul class="space-y-0.5">
							{#each nav.filter((item) => item.group === group) as item (item.href)}
								<li>
									<a
										href={hrefFor(item.href)}
										class="flex items-center gap-2.5 rounded-lg px-2.5 py-2 text-[13px] font-medium transition-colors {isActive(
											item.href
										)
											? 'bg-brand-50 text-brand-700 dark:bg-brand-500/15 dark:text-brand-300'
											: 'text-zinc-600 hover:bg-zinc-100 hover:text-zinc-900 dark:text-zinc-400 dark:hover:bg-zinc-800 dark:hover:text-zinc-100'}"
										aria-current={isActive(item.href) ? 'page' : undefined}
									>
										<Icon name={item.icon} size={16} />
										{item.label}
									</a>
								</li>
							{/each}
						</ul>
					</div>
				{/each}
			</nav>

			<div class="border-t border-zinc-200 p-3 dark:border-zinc-800">
				<button
					type="button"
					class="flex w-full items-center gap-2.5 rounded-lg px-2.5 py-2 text-[13px] font-medium text-zinc-600 transition-colors hover:bg-zinc-100 hover:text-zinc-900 dark:text-zinc-400 dark:hover:bg-zinc-800 dark:hover:text-zinc-100"
					onclick={logout}
				>
					<Icon name="logout" size={16} />退出登录
				</button>
			</div>
		</aside>

		<div class="flex min-w-0 flex-col">
			<header
				class="sticky top-0 z-20 flex h-14 items-center gap-3 border-b border-zinc-200 bg-zinc-50/85 px-4 backdrop-blur dark:border-zinc-800 dark:bg-zinc-950/80 sm:px-6"
			>
				<button
					type="button"
					class="{iconButton} lg:hidden"
					onclick={() => (mobileOpen = true)}
					aria-label="打开菜单"
				>
					<Icon name="menu" size={18} />
				</button>
				<div class="min-w-0 flex-1">
					<h1 class="truncate text-sm font-semibold tracking-tight">{current.title}</h1>
					<p class="muted hidden truncate text-xs sm:block">{current.subtitle}</p>
				</div>
				<button
					type="button"
					class={iconButton}
					onclick={() => theme.toggle()}
					aria-label={theme.dark ? '切换到浅色主题' : '切换到深色主题'}
					title={theme.dark ? '浅色主题' : '深色主题'}
				>
					<Icon name={theme.dark ? 'sun' : 'moon'} size={17} />
				</button>
			</header>

			<main class="mx-auto w-full max-w-6xl flex-1 px-4 py-5 sm:px-6 sm:py-6">
				{@render children()}
			</main>
		</div>
	</div>
{/if}
