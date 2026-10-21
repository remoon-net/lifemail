<script lang="ts">
	import { toast } from '$lib/toast.svelte';
	import Icon from './Icon.svelte';

	const tones: Record<string, string> = {
		ok: 'border-emerald-200 bg-white text-emerald-800 dark:border-emerald-900 dark:bg-zinc-900 dark:text-emerald-300',
		error: 'border-red-200 bg-white text-red-800 dark:border-red-900 dark:bg-zinc-900 dark:text-red-300',
		info: 'border-zinc-200 bg-white text-zinc-800 dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-200'
	};
</script>

<div
	class="pointer-events-none fixed inset-x-0 bottom-4 z-50 flex flex-col items-center gap-2 px-4 sm:right-4 sm:left-auto sm:items-end"
	aria-live="polite"
	aria-atomic="false"
>
	{#each toast.items as item (item.id)}
		<div
			class="pointer-events-auto flex max-w-sm items-start gap-2 rounded-xl border px-3.5 py-2.5 text-sm shadow-lg {tones[
				item.kind
			]}"
		>
			<Icon
				name={item.kind === 'error' ? 'alert' : item.kind === 'ok' ? 'check' : 'activity'}
				size={16}
				class="mt-0.5 shrink-0"
			/>
			<p class="flex-1">{item.text}</p>
			<button
				type="button"
				class="mt-0.5 shrink-0 opacity-50 hover:opacity-100"
				onclick={() => toast.dismiss(item.id)}
				aria-label="关闭提示"
			>
				<Icon name="x" size={14} />
			</button>
		</div>
	{/each}
</div>
