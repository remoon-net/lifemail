export interface SessionInfo {
	authenticated: boolean;
	version: string;
}

export interface User {
	id: string;
	name: string;
	address: string;
	display_name: string;
	created_at: number;
}

export interface Alias {
	address: string;
	user_id: string;
	is_primary: boolean;
	created_at: number;
}

export interface Domain {
	name: string;
	tls_cert?: string;
	tls_key?: string;
}

export interface Smtp {
	host: string;
	port: number;
	username: string;
	password_set: boolean;
	from: string;
}

export interface Settings {
	domains: Domain[];
	smtp: Smtp;
}

export interface OutboxItem {
	id: number;
	envelope_from: string;
	recipients: string[];
	attempts: number;
	last_error: string;
	next_attempt: number;
	failed: boolean;
	created_at: number;
	size: number;
}

export interface Device {
	user_id: string;
	address: string;
	device_id: string;
	device_type: string;
	protocol: string;
	last_seen: number;
	wipe_requested: boolean;
	created_at: number;
	last_ip: string;
	app_version: string;
}

export interface CalendarRow {
	id: number;
	user_id: string;
	owner: string;
	name: string;
	public: boolean;
	subscribe_url?: string;
}

export interface Stats {
	users: number;
	messages: number;
	devices: number;
	outbox_pending: number;
	outbox_failed: number;
	domains: number;
	smtp_ready: boolean;
	version: string;
	dialect: string;
}

export interface Migration {
	version: number;
	name: string;
	applied: boolean;
	applied_at: number;
}

export interface MigrationsInfo {
	dialect: string;
	items: Migration[];
}
