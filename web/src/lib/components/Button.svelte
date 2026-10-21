<script lang="ts">
	import type { Snippet } from 'svelte';

	type Props = {
		variant?: 'primary' | 'secondary' | 'ghost' | 'danger';
		size?: 'sm' | 'md';
		type?: 'button' | 'submit' | 'reset';
		disabled?: boolean;
		title?: string;
		ariaLabel?: string;
		form?: string;
		class?: string;
		children: Snippet;
		onclick?: (event: MouseEvent) => void;
	};

	let {
		variant = 'primary',
		size = 'md',
		type = 'button',
		disabled = false,
		title,
		ariaLabel,
		form,
		class: extra = '',
		children,
		onclick
	}: Props = $props();

	const base =
		'inline-flex items-center justify-center gap-1.5 rounded-lg font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-50';
	const variants: Record<string, string> = {
		primary:
			'bg-brand-600 text-white hover:bg-brand-700 dark:bg-brand-500 dark:text-zinc-950 dark:hover:bg-brand-400',
		secondary:
			'border border-zinc-300 bg-white text-zinc-700 hover:bg-zinc-50 dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-200 dark:hover:bg-zinc-800',
		ghost:
			'text-zinc-600 hover:bg-zinc-100 hover:text-zinc-900 dark:text-zinc-400 dark:hover:bg-zinc-800 dark:hover:text-zinc-100',
		danger:
			'border border-red-200 bg-red-50 text-red-700 hover:bg-red-100 dark:border-red-900/60 dark:bg-red-950/40 dark:text-red-300 dark:hover:bg-red-950'
	};
	const sizes: Record<string, string> = {
		sm: 'h-8 px-3 text-[13px]',
		md: 'h-9.5 px-4 text-sm'
	};
</script>

<button
	{type}
	{disabled}
	{title}
	{form}
	aria-label={ariaLabel}
	class="{base} {variants[variant]} {sizes[size]} {extra}"
	onclick={onclick}
>
	{@render children()}
</button>
