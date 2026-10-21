<script lang="ts">
	import type { Snippet } from 'svelte';
	import Icon from './Icon.svelte';
	import type { IconName } from '#lib/icons';

	type Props = {
		icon?: IconName;
		title: string;
		description?: string;
		action?: Snippet;
	};

	let { icon = 'mail', title, description, action }: Props = $props();
</script>

<div class="flex flex-col items-center justify-center gap-2 px-4 py-10 text-center">
	<span
		class="flex h-10 w-10 items-center justify-center rounded-full bg-zinc-100 text-zinc-400 dark:bg-zinc-800 dark:text-zinc-500"
	>
		<Icon name={icon} size={18} />
	</span>
	<p class="text-sm font-medium">{title}</p>
	{#if description}<p class="muted max-w-sm text-xs">{description}</p>{/if}
	{#if action}<div class="mt-1">{@render action()}</div>{/if}
</div>
