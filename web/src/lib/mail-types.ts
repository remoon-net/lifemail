export type UserProfile = {
	id: string;
	name: string;
	address: string;
	displayName: string;
};

export type MailFolder = {
	id: number;
	parentId: number;
	name: string;
	type: number;
	unread: number;
	total: number;
};

export type Conversation = {
	key: string;
	subject: string;
	from: { name: string; address: string };
	to: string;
	lastAt: number;
	count: number;
	unread: number;
	hasAttachments: boolean;
	preview: string;
	latestMessageId: number;
};

export type Attachment = {
	id: number;
	fileName: string;
	contentType: string;
	size: number;
	isInline: boolean;
};

export type MailMessage = {
	id: number;
	folderId: number;
	subject: string;
	fromName: string;
	fromAddr: string;
	to: string;
	cc: string;
	date: number;
	isRead: boolean;
	hasAttachments: boolean;
	bodyText: string;
	bodyHtml: string;
	attachments: Attachment[];
};

export type CalendarRow = {
	id: number;
	name: string;
	public: boolean;
	subscribeUrl?: string;
	eventCount: number;
};

export type CalendarEvent = {
	id: number;
	uid: string;
	subject: string;
	location: string;
	body: string;
	start: number;
	end: number;
	allDay: boolean;
	busyStatus: number;
	recurring: boolean;
	organizer: string;
	organizerAddress: string;
};

export type Contact = {
	id: number;
	display: string;
	fileAs: string;
	firstName: string;
	lastName: string;
	middleName: string;
	company: string;
	department: string;
	jobTitle: string;
	email1: string;
	email2: string;
	email3: string;
	homePhone: string;
	mobilePhone: string;
	businessPhone: string;
	businessFax: string;
	homeStreet: string;
	homeCity: string;
	homeState: string;
	homePostalCode: string;
	homeCountry: string;
	businessStreet: string;
	businessCity: string;
	businessState: string;
	businessPostalCode: string;
	businessCountry: string;
	body: string;
};
