<script lang="ts">
	type Props = {
		html: string;
		text: string;
	};

	let { html, text }: Props = $props();
	let frame = $state<HTMLIFrameElement | null>(null);
	let wrapper = $state<HTMLDivElement | null>(null);

	// The HTML is rendered inside a sandboxed frame: no scripts, no forms, no
	// top navigation. The parent CSP also drops script execution, so a hostile
	// message cannot run anything even if the sandbox were bypassed.
	const document_ = $derived.by(() => `<!doctype html>
<html lang="und"><head><meta charset="utf-8"><meta name="referrer" content="no-referrer">
<style>
	html,body{margin:0;padding:0;height:auto;overflow:hidden;background:#fff;color:#18181b;
		font:14px/1.65 -apple-system,BlinkMacSystemFont,"Segoe UI",system-ui,sans-serif;
		word-break:break-word;overflow-wrap:anywhere}
	img{max-width:100%;height:auto}
	a{color:#4f46e5}
	table{max-width:100%;border-collapse:collapse}
	blockquote{margin:0;padding-left:12px;border-left:2px solid #e4e4e7;color:#52525b}
	pre{white-space:pre-wrap}
</style></head><body>${html}</body></html>`);

	function resize() {
		const doc = frame?.contentDocument;
		const body = doc?.body;
		if (!body || !frame) return;
		// The body is height:auto, so its scrollHeight is the content height. The
		// guard stops the resize observer from feeding its own output back in.
		const height = Math.min(Math.ceil(body.scrollHeight) + 16, 20000);
		if (Math.abs((parseFloat(frame.style.height) || 0) - height) <= 2) return;
		frame.style.height = `${Math.max(height, 60)}px`;
	}

	// A narrower viewport reflows the message, so the height has to follow the
	// frame's width as well as its onload event (images change it later too).
	$effect(() => {
		if (!wrapper) return;
		const observer = new ResizeObserver(() => resize());
		observer.observe(wrapper);
		return () => observer.disconnect();
	});
</script>

<div bind:this={wrapper}>
	{#if html}
		<iframe
			bind:this={frame}
			title="邮件正文"
			sandbox="allow-same-origin"
			srcdoc={document_}
				onload={() => {
					resize();
					// Images and web fonts settle after onload.
					setTimeout(resize, 300);
				}}
			class="w-full rounded-lg border-0 bg-white"
			style="height: 60px"
		></iframe>
	{:else}
		<pre class="whitespace-pre-wrap break-words font-sans text-[13px] leading-6 text-zinc-700 dark:text-zinc-200"
			>{text || '（无正文）'}</pre
		>
	{/if}
</div>
