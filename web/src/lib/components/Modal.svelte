<script lang="ts">
	import type { Snippet } from 'svelte';
	import Icon from './Icon.svelte';

	type Props = {
		open?: boolean;
		title: string;
		description?: string;
		onclose: () => void;
		children?: Snippet;
		footer?: Snippet;
		width?: string;
	};

	let { open = false, title, description, onclose, children, footer, width = 'max-w-lg' }: Props =
		$props();

	let dialog = $state<HTMLDialogElement | null>(null);

	$effect(() => {
		const element = dialog;
		if (!element) return;
		if (open && !element.open) element.showModal();
		if (!open && element.open) element.close();
	});
</script>

<dialog
	bind:this={dialog}
	oncancel={(event) => {
		event.preventDefault();
		onclose();
	}}
	onclick={(event) => {
		if (event.target === dialog) onclose();
	}}
	class="{width} m-auto w-[calc(100%-2rem)] rounded-2xl border border-zinc-200 bg-white p-0 text-zinc-900 shadow-2xl backdrop:bg-zinc-950/50 backdrop:backdrop-blur-sm dark:border-zinc-800 dark:bg-zinc-900 dark:text-zinc-100"
>
	<div class="flex items-start justify-between gap-4 px-5 pt-4 pb-3">
		<div>
			<h2 class="text-base font-semibold">{title}</h2>
			{#if description}<p class="muted mt-0.5 text-xs">{description}</p>{/if}
		</div>
		<button
			type="button"
			class="-mr-1 inline-flex h-8 w-8 items-center justify-center rounded-lg text-zinc-400 hover:bg-zinc-100 hover:text-zinc-700 dark:hover:bg-zinc-800 dark:hover:text-zinc-200"
			onclick={onclose}
			aria-label="关闭"
		>
			<Icon name="x" size={16} />
		</button>
	</div>
	{#if children}
		<div class="px-5 pb-5">
			{@render children()}
		</div>
	{/if}
	{#if footer}
		<div
			class="flex items-center justify-end gap-2 border-t border-zinc-200 bg-zinc-50/60 px-5 py-3 dark:border-zinc-800 dark:bg-zinc-900/60"
		>
			{@render footer()}
		</div>
	{/if}
</dialog>
