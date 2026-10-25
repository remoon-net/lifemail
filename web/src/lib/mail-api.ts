import type {
	CalendarEvent,
	CalendarRow,
	Contact,
	ContactInput,
	Conversation,
	EventInput,
	MailFolder,
	MailMessage,
	MailSearchHit,
	ReplyTarget,
	UserProfile
} from './mail-types';

const PREFIX = '/api/user';

export class MailApiError extends Error {
	status: number;

	constructor(message: string, status: number) {
		super(message);
		this.name = 'MailApiError';
		this.status = status;
	}

	get unauthorized(): boolean {
		return this.status === 401;
	}
}

type SessionInfo = {
	authenticated: boolean;
	user?: UserProfile;
	version: string;
};

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
	const headers = new Headers(init.headers);
	if (init.body && !headers.has('Content-Type')) {
		headers.set('Content-Type', 'application/json');
	}
	const response = await fetch(PREFIX + path, {
		credentials: 'same-origin',
		...init,
		headers
	});
	const text = await response.text();
	let data: unknown = null;
	if (text) {
		try {
			data = JSON.parse(text);
		} catch {
			data = null;
		}
	}
	if (!response.ok) {
		if (response.status === 401 && path !== '/session' && typeof window !== 'undefined') {
			window.dispatchEvent(new CustomEvent('lifemail:user-unauthorized'));
		}
		const message =
			data && typeof data === 'object' && 'error' in data
				? String((data as { error: unknown }).error)
				: response.statusText || `HTTP ${response.status}`;
		throw new MailApiError(message, response.status);
	}
	return data as T;
}

function json(body: unknown): RequestInit {
	return { body: JSON.stringify(body) };
}

export const mailApi = {
	session: () => request<SessionInfo>('/session'),
	login: (login: string, password: string) =>
		request<SessionInfo>('/session', { method: 'POST', ...json({ login, password }) }),
	logout: () => request<{ ok: boolean }>('/session', { method: 'DELETE' }),

	folders: () => request<{ folders: MailFolder[] }>('/mail/folders'),
	conversations: (folderId: number | null, limit = 50) =>
		request<{ folderId: number; conversations: Conversation[] }>(
			`/mail/conversations?limit=${limit}${folderId ? `&folder=${folderId}` : ''}`
		),
	thread: (folderId: number, key: string) =>
		request<{
			folderId: number;
			sentFolderId: number;
			messages: MailMessage[];
			replyTarget?: ReplyTarget;
		}>(`/mail/thread?folder=${folderId}&key=${encodeURIComponent(key)}`),
	message: (id: number) => request<{ message: MailMessage }>(`/mail/messages/${id}`),
	markRead: (id: number) =>
		request<{ ok: boolean; read: boolean }>(`/mail/messages/${id}/read`, { method: 'POST' }),
	search: (query: string, folderId?: number | null, limit = 50) =>
		request<{ query: string; total: number; hits: MailSearchHit[] }>(
			`/mail/search?q=${encodeURIComponent(query)}&limit=${limit}` +
				(folderId ? `&folder=${folderId}` : '')
		),
	reply: (input: {
		messageId: number;
		body: string;
		subject: string;
		to: string[];
		cc: string[];
	}) =>
		request<{ ok: boolean; queued: boolean }>('/mail/reply', {
			method: 'POST',
			...json(input)
		}),
	attachmentUrl: (id: number) => `${PREFIX}/mail/attachments/${id}`,

	calendars: () => request<{ calendars: CalendarRow[] }>('/calendars'),
	createCalendar: (name: string) =>
		request<{ calendar: CalendarRow }>('/calendars', { method: 'POST', ...json({ name }) }),
	renameCalendar: (id: number, name: string) =>
		request<{ calendar: CalendarRow }>(`/calendars/${id}`, {
			method: 'PATCH',
			...json({ name })
		}),
	setCalendarPublic: (id: number, value: boolean) =>
		request<{ calendar: CalendarRow }>(`/calendars/${id}/public`, {
			method: 'PUT',
			...json({ public: value })
		}),
	calendarEvents: (id: number, from: number, to: number) =>
		request<{ events: CalendarEvent[] }>(`/calendars/${id}/events?from=${from}&to=${to}`),
	createEvent: (calendarId: number, body: EventInput) =>
		request<{ event: CalendarEvent }>(`/calendars/${calendarId}/events`, {
			method: 'POST',
			...json(body)
		}),
	updateEvent: (calendarId: number, eventId: number, body: EventInput) =>
		request<{ event: CalendarEvent }>(`/calendars/${calendarId}/events/${eventId}`, {
			method: 'PATCH',
			...json(body)
		}),
	deleteEvent: (calendarId: number, eventId: number) =>
		request<{ ok: boolean }>(`/calendars/${calendarId}/events/${eventId}`, {
			method: 'DELETE'
		}),

	contacts: (q = '', limit = 100) =>
		request<{ contacts: Contact[] }>(
			`/contacts?limit=${limit}${q ? `&q=${encodeURIComponent(q)}` : ''}`
		),
	contact: (id: number) => request<{ contact: Contact }>(`/contacts/${id}`),
	createContact: (body: ContactInput) =>
		request<{ contact: Contact }>('/contacts', { method: 'POST', ...json(body) }),
	updateContact: (id: number, body: ContactInput) =>
		request<{ contact: Contact }>(`/contacts/${id}`, { method: 'PATCH', ...json(body) }),
	deleteContact: (id: number) =>
		request<{ ok: boolean }>(`/contacts/${id}`, { method: 'DELETE' })
};
