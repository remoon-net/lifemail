import type {
	Alias,
	CalendarRow,
	Device,
	Migration,
	MigrationsInfo,
	OutboxItem,
	SessionInfo,
	Settings,
	Stats,
	User
} from './types';

const PREFIX = '/api/admin';

export class ApiError extends Error {
	status: number;

	constructor(message: string, status: number) {
		super(message);
		this.name = 'ApiError';
		this.status = status;
	}

	get unauthorized(): boolean {
		return this.status === 401;
	}
}

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
		if (response.status === 401 && path !== '/login' && typeof window !== 'undefined') {
			// The session expired or was revoked in another tab. The layout
			// listens for this and sends the user back to the login page.
			window.dispatchEvent(new CustomEvent('lifemail:unauthorized'));
		}
		const message =
			data && typeof data === 'object' && 'error' in data
				? String((data as { error: unknown }).error)
				: response.statusText || `HTTP ${response.status}`;
		throw new ApiError(message, response.status);
	}
	return data as T;
}

function json(body: unknown): RequestInit {
	return { body: JSON.stringify(body) };
}

export const api = {
	session: () => request<SessionInfo>('/session'),
	login: (token: string) =>
		request<SessionInfo>('/login', { method: 'POST', ...json({ token }) }),
	logout: () => request<{ ok: boolean }>('/logout', { method: 'POST' }),

	stats: () => request<Stats>('/stats'),
	migrations: () => request<MigrationsInfo>('/migrations'),

	settings: () => request<Settings>('/settings'),
	saveSettings: (body: unknown) => request<Settings>('/settings', { method: 'PUT', ...json(body) }),

	users: () => request<{ users: User[] }>('/users'),
	createUser: (body: { name: string; address: string; display_name: string }) =>
		request<{ user: User; password?: string }>('/users', { method: 'POST', ...json(body) }),
	updateUser: (
		id: string,
		body: { display_name?: string; password?: string; address?: string }
	) => request<{ user: User; password?: string }>(`/users/${encodeURIComponent(id)}`, {
		method: 'PATCH',
		...json(body)
	}),
	deleteUser: (id: string) =>
		request<{ ok: boolean }>(`/users/${encodeURIComponent(id)}`, { method: 'DELETE' }),

	aliases: () => request<{ aliases: Alias[] }>('/aliases'),
	createAlias: (body: { address: string; user_id: string }) =>
		request<{ alias: Alias }>('/aliases', { method: 'POST', ...json(body) }),
	deleteAlias: (address: string) =>
		request<{ ok: boolean }>(`/aliases/${encodeURIComponent(address)}`, { method: 'DELETE' }),

	outbox: (failedOnly: boolean) =>
		request<{ items: OutboxItem[] }>(`/outbox?limit=200${failedOnly ? '&failed=1' : ''}`),
	retryOutbox: (id: number) =>
		request<{ ok: boolean }>(`/outbox/${id}/retry`, { method: 'POST' }),
	deleteOutbox: (id: number) => request<{ ok: boolean }>(`/outbox/${id}`, { method: 'DELETE' }),

	devices: () => request<{ items: Device[] }>('/devices'),
	wipeDevice: (userID: string, deviceID: string) =>
		request<{ ok: boolean }>(
			`/devices/${encodeURIComponent(userID)}/${encodeURIComponent(deviceID)}/wipe`,
			{ method: 'POST' }
		),
	deleteDevice: (userID: string, deviceID: string) =>
		request<{ ok: boolean }>(
			`/devices/${encodeURIComponent(userID)}/${encodeURIComponent(deviceID)}`,
			{ method: 'DELETE' }
		),

	calendars: () => request<{ items: CalendarRow[] }>('/calendars'),
	setCalendarPublic: (id: number, value: boolean) =>
		request<{ id: number; public: boolean }>(`/calendars/${id}/public`, {
			method: 'PUT',
			...json({ public: value })
		}),

	maintenance: (vacuum: boolean) =>
		request<{ changes_pruned: number }>('/maintenance', {
			method: 'POST',
			...json({ vacuum })
		})
};

export type { Migration };
