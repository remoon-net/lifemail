package eas

// ActiveSync WBXML code pages. Only the pages this server implements are
// listed; unknown pages/tokens decode to synthetic names and are ignored.
const (
	pageAirSync           = 0
	pageContacts          = 1
	pageEmail             = 2
	pageCalendar          = 4
	pageMove              = 5
	pageGetItemEstimate   = 6
	pageFolderHierarchy   = 7
	pageMeetingResponse   = 8
	pageResolveRecipients = 10
	pagePing              = 13
	pageProvision         = 14
	pageSearch            = 15
	pageGAL               = 16
	pageAirSyncBase       = 17
	pageSettings          = 18
	pageItemOperations    = 20
	pageComposeMail       = 21
	pageEmail2            = 22
)

// tokenPages maps a code page to its element names. Element 0 is token 0x05.
var tokenPages = map[int][]string{
	pageAirSync: {
		"Sync", "Responses", "Add", "Change", "Delete", "Fetch", "SyncKey",
		"ClientId", "ServerId", "Status", "Collection", "Class", "Version",
		"CollectionId", "GetChanges", "MoreAvailable", "WindowSize", "Commands",
		"Options", "FilterType", "Truncation", "RtfTruncation", "Conflict",
		"Collections", "ApplicationData", "DeletesAsMoves", "NotifyGUID",
		"Supported", "SoftDelete", "MIMESupport", "MIMETruncation", "Wait",
		"Limit", "Partial", "ConversationMode", "MaxItems", "HeartbeatInterval",
	},
	pageContacts: {
		"Anniversary", "AssistantName", "AssistantTelephoneNumber", "Birthday",
		"Body", "BodySize", "BodyTruncated", "Business2PhoneNumber",
		"BusinessAddressCity", "BusinessAddressCountry", "BusinessAddressPostalCode",
		"BusinessAddressState", "BusinessAddressStreet", "BusinessFaxNumber",
		"BusinessPhoneNumber", "CarPhoneNumber", "Categories", "Category",
		"Children", "Child", "CompanyName", "Department", "Email1Address",
		"Email2Address", "Email3Address", "FileAs", "FirstName",
		"Home2PhoneNumber", "HomeAddressCity", "HomeAddressCountry",
		"HomeAddressPostalCode", "HomeAddressState", "HomeAddressStreet",
		"HomeFaxNumber", "HomePhoneNumber", "JobTitle", "LastName", "MiddleName",
		"MobilePhoneNumber", "OfficeLocation", "OtherAddressCity",
		"OtherAddressCountry", "OtherAddressPostalCode", "OtherAddressState",
		"OtherAddressStreet", "PagerNumber", "RadioPhoneNumber", "Spouse",
		"Suffix", "Title", "WebPage", "YomiCompanyName", "YomiFirstName",
		"YomiLastName", "CompressedRTF", "Picture", "Alias", "WeightedRank",
	},
	pageEmail: {
		"Attachment", "Attachments", "AttName", "AttSize", "Att0Id", "AttMethod",
		"AttRemoved", "Body", "BodySize", "BodyTruncated", "DateReceived",
		"DisplayName", "DisplayTo", "Importance", "MessageClass", "Subject",
		"Read", "To", "Cc", "From", "ReplyTo", "AllDayEvent", "Categories",
		"Category", "DTStamp", "EndTime", "InstanceType", "BusyStatus", "Location",
		"MeetingRequest", "Organizer", "RecurrenceId", "Reminder",
		"ResponseRequested", "Recurrences", "Recurrence", "Recurrence_Type",
		"Recurrence_Until", "Recurrence_Occurrences", "Recurrence_Interval",
		"Recurrence_DayOfWeek", "Recurrence_DayOfMonth", "Recurrence_WeekOfMonth",
		"Recurrence_MonthOfYear", "StartTime", "Sensitivity", "TimeZone",
		"GlobalObjId", "ThreadTopic", "MIMEData", "MIMETruncated", "MIMESize",
		"InternetCPID", "Flag", "FlagStatus", "ContentClass", "FlagType",
		"CompleteTime", "DisallowNewTimeProposal",
	},
	pageCalendar: {
		"TimeZone", "AllDayEvent", "Attendees", "Attendee", "Email", "Name",
		"Body", "BodyTruncated", "BusyStatus", "Categories", "Category",
		"CompressedRTF", "DtStamp", "EndTime", "Exception", "Exceptions",
		"Deleted", "ExceptionStartTime", "Location", "MeetingStatus",
		"OrganizerEmail", "OrganizerName", "Recurrence", "RecurrenceType",
		"RecurrenceUntil", "RecurrenceOccurrences", "RecurrenceInterval",
		"RecurrenceDayOfWeek", "RecurrenceDayOfMonth", "RecurrenceWeekOfMonth",
		"RecurrenceMonthOfYear", "Reminder", "Sensitivity", "Subject", "StartTime",
		"UID", "AttendeeStatus", "AttendeeType",
		"", "", "", "", "", "", "", "",
		"DisallowNewTimeProposal", "ResponseRequested", "AppointmentReplyTime",
		"ResponseType", "CalendarType", "IsLeapMonth", "FirstDayOfWeek",
	},
	pageMove: {
		"MoveItems", "Move", "SrcMsgId", "SrcFldId", "DstFldId", "Response",
		"Status", "DstMsgId",
	},
	pageGetItemEstimate: {
		"GetItemEstimate", "Version", "Folders", "Folder", "FolderType",
		"FolderId", "DateTime", "Estimate", "Response", "Status",
	},
	pageFolderHierarchy: {
		"Folders", "Folder", "DisplayName", "ServerId", "ParentId", "Type",
		"Response", "Status", "ContentClass", "Changes", "Add", "Delete",
		"Update", "SyncKey", "FolderCreate", "FolderDelete", "FolderUpdate",
		"FolderSync", "Count",
	},
	pagePing: {
		"Ping", "AutdState", "Status", "HeartbeatInterval", "Folders", "Folder",
		"ServerId", "Class", "MaxFolders", "Version",
	},
	pageMeetingResponse: {
		"CalendarId", "FolderId", "MeetingResponse", "RequestId", "Request",
		"Result", "Status", "UserResponse", "Version", "InstanceId",
	},
	pageResolveRecipients: {
		"ResolveRecipients", "Response", "Status", "Type", "Recipient",
		"DisplayName", "EmailAddress", "Certificates", "Certificate",
		"MiniCertificate", "Options", "To", "CertificateRetrieval",
		"RecipientCount", "MaxCertificates", "MaxAmbiguousRecipients",
		"CertificateCount", "Availability", "StartTime", "EndTime",
		"MergedFreeBusy", "Picture", "MaxSize", "Data", "MaxPictures",
	},
	pageSearch: {
		"Search", "", "Store", "Name", "Query", "Options", "Range",
		"Status", "Response", "Result", "Properties", "Total", "EqualTo",
		"Value", "And", "Or", "FreeText", "", "DeepTraversal", "LongId",
		"RebuildResults", "LessThan", "GreaterThan", "Schema", "Supported",
		"UserName", "Password", "ConversationId", "Picture", "MaxSize",
		"MaxPictures",
	},
	pageGAL: {
		"DisplayName", "Phone", "Office", "Title", "Company", "Alias",
		"FirstName", "LastName", "HomePhone", "MobilePhone", "EmailAddress",
		"Picture", "Status",
	},
	pageProvision: {
		"Provision", "Policies", "Policy", "PolicyType", "PolicyKey", "Data",
		"Status", "RemoteWipe", "EASProvisionDoc", "DevicePasswordEnabled",
		"AlphanumericDevicePasswordRequired", "RequireStorageCardEncryption",
		"PasswordRecoveryEnabled", "DocumentBrowseEnabled",
		"AttachmentsEnabled", "MinDevicePasswordLength",
		"MaxInactivityTimeDeviceLock", "MaxDevicePasswordFailedAttempts",
		"MaxAttachmentSize", "AllowSimpleDevicePassword",
		"DevicePasswordExpiration", "DevicePasswordHistory", "AllowStorageCard",
		"AllowCamera", "RequireDeviceEncryption", "AllowUnsignedApplications",
		"AllowUnsignedInstallationPackages",
		"MinDevicePasswordComplexCharacters", "AllowWiFi", "AllowTextMessaging",
		"AllowPOPIMAPEmail", "AllowBluetooth", "AllowIrDA",
		"RequireManualSyncWhenRoaming", "AllowDesktopSync",
		"MaxCalendarAgeFilter", "AllowHTMLEmail", "MaxEmailAgeFilter",
		"MaxEmailBodyTruncationSize", "MaxEmailHTMLBodyTruncationSize",
		"RequireSignedSMIMEMessages", "RequireEncryptedSMIMEMessages",
		"RequireSignedSMIMEAlgorithm", "RequireEncryptionSMIMEAlgorithm",
		"AllowSMIMEEncryptionAlgorithmNegotiation", "AllowSMIMESoftCerts",
		"AllowBrowser", "AllowConsumerEmail", "AllowRemoteDesktop",
		"AllowInternetSharing", "UnapprovedInROMApplicationList",
		"ApplicationName", "ApprovedApplicationList", "Hash",
	},
	pageAirSyncBase: {
		"BodyPreference", "Type", "Truncation", "AllOrNone", "",
		"Body", "Data", "EstimatedDataSize", "Truncated", "Attachments",
		"Attachment", "DisplayName", "FileReference", "Method", "ContentId",
		"ContentLocation", "IsInline", "NativeBodyType", "ContentType", "Preview",
		"BodyPartPreference", "BodyPart", "Status",
	},
	pageSettings: {
		"Settings", "Status", "Get", "Set", "Oof", "OofState", "StartTime",
		"EndTime", "OofMessage", "AppliesToInternal", "AppliesToExternalKnown",
		"AppliesToExternalUnknown", "Enabled", "ReplyMessage", "BodyType",
		"DevicePassword", "Password", "DeviceInformation", "Model", "IMEI",
		"FriendlyName", "OS", "OSLanguage", "PhoneNumber", "UserInformation",
		"EmailAddresses", "SmtpAddress", "UserAgent", "EnableOutboundSMS",
		"MobileOperator", "PrimarySmtpAddress",
	},
	pageItemOperations: {
		"ItemOperations", "Fetch", "Store", "Options", "Range", "Total",
		"Properties", "Data", "Status", "Response", "Version", "Schema", "Part",
		"EmptyFolderContents", "DeleteSubFolders", "UserName", "Password",
		"Move", "DstFldId", "ConversationId", "MoveAlways",
	},
	pageComposeMail: {
		"SendMail", "SmartForward", "SmartReply", "SaveInSentItems",
		"ReplaceMime", "Type", "Source", "FolderId", "ItemId", "LongId",
		"InstanceId", "Mime", "ClientId", "Status", "AccountId",
	},
	pageEmail2: {
		"UmCallerID", "UmUserNotes", "UmAttDuration", "UmAttOrder",
		"ConversationId", "ConversationIndex", "LastVerbExecuted",
		"LastVerbExecutionTime", "ReceivedAsBcc", "Sender", "CalendarType",
		"IsLeapMonth", "AccountId", "FirstDayOfWeek", "MeetingMessageType", "",
		"IsDraft", "Bcc", "Send",
	},
}

type wbxmlCodec struct {
	tokens map[int]map[string]byte
	names  map[int]map[byte]string
}

var codec = newCodec()

func newCodec() *wbxmlCodec {
	c := &wbxmlCodec{
		tokens: make(map[int]map[string]byte, len(tokenPages)),
		names:  make(map[int]map[byte]string, len(tokenPages)),
	}
	for page, list := range tokenPages {
		c.tokens[page] = make(map[string]byte, len(list))
		c.names[page] = make(map[byte]string, len(list))
		for i, name := range list {
			if name == "" {
				continue
			}
			tok := byte(i + 5)
			c.tokens[page][name] = tok
			c.names[page][tok] = name
		}
	}
	return c
}

func (c *wbxmlCodec) Token(page int, name string) (byte, bool) {
	m, ok := c.tokens[page]
	if !ok {
		return 0, false
	}
	tok, ok := m[name]
	return tok, ok
}

func (c *wbxmlCodec) Name(page int, token byte) (string, bool) {
	m, ok := c.names[page]
	if !ok {
		return "", false
	}
	name, ok := m[token]
	return name, ok
}
