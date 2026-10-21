<script lang="ts">
	import { onMount } from 'svelte';
	import Badge from '$lib/components/Badge.svelte';
	import Button from '$lib/components/Button.svelte';
	import Card from '$lib/components/Card.svelte';
	import EmptyState from '$lib/components/EmptyState.svelte';
	import Icon from '$lib/components/Icon.svelte';
	import Modal from '$lib/components/Modal.svelte';
	import Skeleton from '$lib/components/Skeleton.svelte';
	import { api } from '$lib/api';
	import { dateTime, relativeTime } from '$lib/format';
	import { toast } from '$lib/toast.svelte';
	import type { Device } from '$lib/types';
	import { iconButton, iconButtonDanger } from '$lib/ui';

	let devices = $state<Device[]>([]);
	let loading = $state(true);
	let wiping = $state<Device | null>(null);
	let removing = $state<Device | null>(null);
	let cleaning = $state(false);

	async function load() {
		loading = true;
		try {
			devices = (await api.devices()).items;
		} catch (err) {
			toast.fail(err, '无法读取设备');
		} finally {
			loading = false;
		}
	}

	onMount(() => {
		void load();
	});

	async function wipe() {
		if (!wiping) return;
		const device = wiping;
		wiping = null;
		try {
			await api.wipeDevice(device.user_id, device.device_id);
			await load();
			toast.ok(`已标记 ${device.device_id} 等待擦除`);
		} catch (err) {
			toast.fail(err, '擦除失败');
		}
	}

	async function remove() {
		if (!removing) return;
		const device = removing;
		removing = null;
		try {
			await api.deleteDevice(device.user_id, device.device_id);
			await load();
			toast.ok(`已移除 ${device.device_id}`);
		} catch (err) {
			toast.fail(err, '移除失败');
		}
	}

	async function cleanup() {
		cleaning = true;
		try {
			const result = await api.maintenance(false);
			toast.ok(`已清理 ${result.changes_pruned} 条变更记录`);
		} catch (err) {
			toast.fail(err, '清理失败');
		} finally {
			cleaning = false;
		}
	}
</script>

<div class="space-y-4">
	<div class="flex flex-wrap items-center justify-between gap-2">
		<p class="muted text-xs">设备通过 EAS Provision 注册；擦除指令在设备下次同步时下发。</p>
		<div class="flex items-center gap-2">
			<Button variant="secondary" size="sm" onclick={cleanup} disabled={cleaning}>
				<Icon name="refresh" size={14} class={cleaning ? 'animate-spin' : ''} />清理变更记录
			</Button>
			<Button variant="secondary" size="sm" onclick={load}>
				<Icon name="refresh" size={14} />刷新
			</Button>
		</div>
	</div>

	<Card padding="none">
		{#if loading}
			<div class="p-4"><Skeleton rows={4} /></div>
		{:else if devices.length === 0}
			<EmptyState icon="device" title="还没有设备同步过" description="客户端第一次连接 EAS 后会自动出现在这里。" />
		{:else}
			<ul class="divide-y divide-zinc-100 dark:divide-zinc-800">
				{#each devices as device (`${device.user_id}/${device.device_id}`)}
					<li class="flex flex-wrap items-center gap-3 px-4 py-3">
						<span
							class="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-zinc-100 text-zinc-500 dark:bg-zinc-800 dark:text-zinc-400"
						>
							<Icon name="device" size={16} />
						</span>
						<div class="min-w-0 flex-1">
							<div class="flex flex-wrap items-center gap-2">
								<span class="text-[13px] font-medium">{device.address || device.user_id}</span>
								<span class="muted font-mono text-[11px]">{device.device_id}</span>
								{#if device.wipe_requested}
									<Badge tone="danger">等待擦除</Badge>
								{/if}
							</div>
							<p class="muted mt-0.5 text-[11px]">
								{device.device_type || '未知设备'}
								{#if device.protocol}· {device.protocol}{/if}
								{#if device.last_ip}· {device.last_ip}{/if}
								· 最近活跃 {relativeTime(device.last_seen)}
								{#if device.created_at}· 首次 {dateTime(device.created_at).slice(0, 10)}{/if}
							</p>
						</div>
						<div class="flex shrink-0 items-center gap-0.5">
							<button
								type="button"
								class={iconButton}
								title="远程擦除"
								aria-label={`擦除 ${device.device_id}`}
								onclick={() => (wiping = device)}
								disabled={device.wipe_requested}
							>
								<Icon name="alert" size={15} />
							</button>
							<button
								type="button"
								class={iconButtonDanger}
								title="移除设备"
								aria-label={`移除 ${device.device_id}`}
								onclick={() => (removing = device)}
							>
								<Icon name="trash" size={15} />
							</button>
						</div>
					</li>
				{/each}
			</ul>
		{/if}
	</Card>
</div>

<Modal
	open={wiping !== null}
	title="远程擦除设备"
	description={wiping
		? `向 ${wiping.device_id} 发送擦除指令？设备下次同步时会清空本地邮件与账户数据。`
		: ''}
	onclose={() => (wiping = null)}
>
	{#snippet footer()}
		<Button variant="secondary" onclick={() => (wiping = null)}>取消</Button>
		<Button variant="danger" onclick={wipe}>发送擦除</Button>
	{/snippet}
</Modal>

<Modal
	open={removing !== null}
	title="移除设备"
	description={removing ? `移除 ${removing.device_id}？该设备需要重新配置账户。` : ''}
	onclose={() => (removing = null)}
>
	{#snippet footer()}
		<Button variant="secondary" onclick={() => (removing = null)}>取消</Button>
		<Button variant="danger" onclick={remove}>移除</Button>
	{/snippet}
</Modal>
