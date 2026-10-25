<script lang="ts">
	import { onMount } from 'svelte';
	import Badge from '#lib/components/Badge.svelte';
	import Button from '#lib/components/Button.svelte';
	import Card from '#lib/components/Card.svelte';
	import EmptyState from '#lib/components/EmptyState.svelte';
	import Icon from '#lib/components/Icon.svelte';
	import Skeleton from '#lib/components/Skeleton.svelte';
	import { api } from '#lib/api';
	import { toast } from '#lib/toast.svelte';
	import type { Domain } from '#lib/types';
	import { iconButtonDanger, monoInput } from '#lib/ui';

	let domains = $state<Domain[]>([]);
	let loading = $state(true);
	let saving = $state(false);

	async function load() {
		loading = true;
		try {
			const settings = await api.settings();
			domains = settings.domains.map((d) => ({ ...d }));
		} catch (err) {
			toast.fail(err, '无法读取域名');
		} finally {
			loading = false;
		}
	}

	onMount(() => {
		void load();
	});

	function add() {
		domains = [...domains, { name: '', tls_cert: '', tls_key: '' }];
	}

	function remove(index: number) {
		domains = domains.filter((_, i) => i !== index);
	}

	async function save() {
		const payload = domains
			.map((d) => ({
				name: d.name.trim(),
				tls_cert: (d.tls_cert ?? '').trim(),
				tls_key: (d.tls_key ?? '').trim()
			}))
			.filter((d) => d.name);
		saving = true;
		try {
			const settings = await api.saveSettings({ domains: payload });
			domains = settings.domains.map((d) => ({ ...d }));
			toast.ok('域名已保存');
		} catch (err) {
			toast.fail(err, '保存失败');
		} finally {
			saving = false;
		}
	}
</script>

<div class="space-y-4">
	<div class="flex flex-wrap items-center justify-between gap-2">
		<p class="muted text-xs">
			收信域由 Cloudflare Email Routing 转发到本服务；证书用于 IMAPS 993 与 SMTPS 465。
		</p>
		<div class="flex items-center gap-2">
			<Button variant="secondary" size="sm" onclick={add}>
				<Icon name="plus" size={14} />添加域名
			</Button>
			<Button size="sm" onclick={save} disabled={saving}>
				{saving ? '保存中…' : '保存'}
			</Button>
		</div>
	</div>

	{#if loading}
		<Skeleton rows={3} />
	{:else if domains.length === 0}
		<div class="surface">
			<EmptyState icon="globe" title="还没有配置域名" description="至少配置一个域名，客户端才能登录与收信。">
				{#snippet action()}
					<Button size="sm" onclick={add}>添加域名</Button>
				{/snippet}
			</EmptyState>
		</div>
	{:else}
		<div class="grid gap-3 lg:grid-cols-2">
			{#each domains as domain, index (index)}
				<Card>
					<div class="space-y-3">
						<div class="flex items-center gap-2">
							<span
								class="flex h-8 w-8 items-center justify-center rounded-lg bg-brand-50 text-brand-600 dark:bg-brand-500/15 dark:text-brand-300"
							>
								<Icon name="globe" size={15} />
							</span>
							<input
								class="{monoInput} flex-1"
								bind:value={domain.name}
								placeholder="example.com"
								aria-label="域名"
								spellcheck="false"
							/>
							{#if domain.tls_cert && domain.tls_key}
								<Badge tone="ok">证书</Badge>
							{:else}
								<Badge tone="warn">无证书</Badge>
							{/if}
							<button
								type="button"
								class={iconButtonDanger}
								onclick={() => remove(index)}
								aria-label={`删除域名 ${domain.name || index + 1}`}
								title="删除域名"
							>
								<Icon name="trash" size={15} />
							</button>
						</div>
						<input
							class={monoInput}
							bind:value={domain.tls_cert}
							placeholder="/etc/lifemail/fullchain.pem"
							aria-label="证书路径"
							spellcheck="false"
						/>
						<input
							class={monoInput}
							bind:value={domain.tls_key}
							placeholder="/etc/lifemail/key.pem"
							aria-label="私钥路径"
							spellcheck="false"
						/>
					</div>
				</Card>
			{/each}
		</div>
		<p class="muted text-[11px]">
			证书路径在保存时校验：文件不可读或证书与私钥不匹配会拒绝保存。
		</p>
	{/if}
</div>
