/**
 * Server push for the account UI.
 *
 * The Go side exposes `GET /api/user/events` as a Server-Sent Events stream.
 * The first frame of every (re)connection is `ready`, which tells the views
 * that anything may have changed while the stream was down; `mail` frames carry
 * a new inbox message. The stream costs one idle connection per tab and no
 * polling, so keep it open for the whole authenticated session.
 */
export type MailEvent = {
	kind: string;
	action?: string;
	folderId?: number;
	folderType?: number;
	messageId?: number;
	subject?: string;
	from?: string;
	at?: number;
};

const STREAM_PATH = '/api/user/events';
const MIN_RETRY = 2000;
const MAX_RETRY = 30000;

function decode(raw: unknown): MailEvent | null {
	if (typeof raw !== 'string' || raw === '') return null;
	try {
		const parsed = JSON.parse(raw) as MailEvent;
		return typeof parsed?.kind === 'string' ? parsed : null;
	} catch {
		return null;
	}
}

class MailEventStore {
	/** True while the stream is open. */
	connected = $state(false);
	/**
	 * Bumped when the view has to reload its data: a new message arrived, or a
	 * reconnection may have missed one. The first `ready` of a session does not
	 * bump it, because the page loads its data anyway.
	 */
	revision = $state(0);
	/** Last pushed mail event, cleared on `ready`. */
	last = $state<MailEvent | null>(null);

	#source: EventSource | null = null;
	#stopped = true;
	#connectedBefore = false;
	#attempt = 0;
	#timer: ReturnType<typeof setTimeout> | undefined;

	/** Opens the stream. Calling it twice keeps the existing one. */
	start(): void {
		if (typeof EventSource === 'undefined') return;
		this.#stopped = false;
		if (this.#source) return;
		this.#open();
	}

	/** Closes the stream and cancels a pending reconnect. */
	stop(): void {
		this.#stopped = true;
		this.#connectedBefore = false;
		this.#attempt = 0;
		clearTimeout(this.#timer);
		const source = this.#source;
		this.#source = null;
		source?.close();
		this.connected = false;
		this.last = null;
	}

	#open(): void {
		const source = new EventSource(STREAM_PATH);
		this.#source = source;

		source.addEventListener('open', () => {
			if (source !== this.#source) return;
			this.connected = true;
			this.#attempt = 0;
		});
		source.addEventListener('error', () => {
			if (source !== this.#source) return;
			this.connected = false;
			// A dropped connection is retried by the browser itself; a closed
			// stream (401/429/500) is final and needs our own backoff.
			if (source.readyState !== EventSource.CLOSED) return;
			this.#source = null;
			this.#scheduleReconnect();
		});
		source.addEventListener('ready', () => {
			if (source !== this.#source) return;
			this.connected = true;
			this.last = null;
			// Pushes sent while the stream was down are lost on purpose: the
			// reload triggered here picks up the real state instead.
			if (this.#connectedBefore) this.revision += 1;
			this.#connectedBefore = true;
		});
		source.addEventListener('mail', (event) => {
			if (source !== this.#source) return;
			const payload = decode((event as MessageEvent<string>).data);
			if (!payload) return;
			this.last = payload;
			this.revision += 1;
		});
	}

	#scheduleReconnect(): void {
		if (this.#stopped) return;
		clearTimeout(this.#timer);
		const delay = Math.min(MAX_RETRY, MIN_RETRY * 2 ** this.#attempt);
		this.#attempt += 1;
		this.#timer = setTimeout(() => {
			if (!this.#stopped && !this.#source) this.#open();
		}, delay);
	}
}

export const mailEvents = new MailEventStore();
