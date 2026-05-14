package db

import "github.com/emersion/go-imap/v2"

type MailboxAttr uint32

const (
	// Base attributes
	MailboxAttrNonExistent MailboxAttr = 1 << iota
	MailboxAttrNoInferiors
	MailboxAttrNoSelect
	MailboxAttrHasChildren
	MailboxAttrHasNoChildren
	MailboxAttrMarked
	MailboxAttrUnmarked
	MailboxAttrSubscribed
	MailboxAttrRemote

	// Role (aka. "special-use") attributes
	MailboxAttrAll
	MailboxAttrArchive
	MailboxAttrDrafts
	MailboxAttrFlagged
	MailboxAttrJunk
	MailboxAttrSent
	MailboxAttrTrash
	MailboxAttrImportant
)

const MailboxAttrSpecialUse = 0 |
	MailboxAttrAll |
	MailboxAttrArchive |
	MailboxAttrDrafts |
	MailboxAttrFlagged |
	MailboxAttrJunk |
	MailboxAttrSent |
	MailboxAttrTrash |
	MailboxAttrImportant

var MailboxAttrMap = map[MailboxAttr]imap.MailboxAttr{
	// Base attributes
	MailboxAttrNonExistent:   imap.MailboxAttrNonExistent,
	MailboxAttrNoInferiors:   imap.MailboxAttrNoInferiors,
	MailboxAttrNoSelect:      imap.MailboxAttrNoSelect,
	MailboxAttrHasChildren:   imap.MailboxAttrHasChildren,
	MailboxAttrHasNoChildren: imap.MailboxAttrHasNoChildren,
	MailboxAttrMarked:        imap.MailboxAttrMarked,
	MailboxAttrUnmarked:      imap.MailboxAttrUnmarked,
	MailboxAttrSubscribed:    imap.MailboxAttrSubscribed,
	MailboxAttrRemote:        imap.MailboxAttrRemote,
	// Role (aka. "special-use") attributes
	MailboxAttrAll:       imap.MailboxAttrAll,
	MailboxAttrArchive:   imap.MailboxAttrArchive,
	MailboxAttrDrafts:    imap.MailboxAttrDrafts,
	MailboxAttrFlagged:   imap.MailboxAttrFlagged,
	MailboxAttrJunk:      imap.MailboxAttrJunk,
	MailboxAttrSent:      imap.MailboxAttrSent,
	MailboxAttrTrash:     imap.MailboxAttrTrash,
	MailboxAttrImportant: imap.MailboxAttrImportant,
}

func (f MailboxAttr) String() string {
	return string(f.Attr())
}
func (f MailboxAttr) Attr() imap.MailboxAttr {
	attr, ok := MailboxAttrMap[f]
	if ok {
		return attr
	}
	return MailboxAttrUnknown
}

func (f MailboxAttr) Attrs() (aa []imap.MailboxAttr) {
	for k, v := range MailboxAttrMap {
		if f&k != 0 {
			aa = append(aa, v)
		}
	}
	return aa
}

const MailboxAttrUnknown = imap.MailboxAttr("unknown")

type MailFlag uint32

const (
	// System flags
	FlagSeen MailFlag = 1 << iota
	FlagAnswered
	FlagFlagged
	FlagDeleted
	FlagDraft

	// Widely used flags
	FlagForwarded
	FlagMDNSent
	FlagJunk
	FlagNotJunk
	FlagPhishing
	FlagImportant

	// Permanent flags
	FlagWildcard
)

const AllMailFlags MailFlag = 0 |
	FlagSeen |
	FlagAnswered |
	FlagFlagged |
	FlagDeleted |
	FlagDraft |
	FlagForwarded |
	FlagMDNSent |
	FlagJunk |
	FlagNotJunk |
	FlagPhishing |
	FlagImportant |
	FlagWildcard

const PermanentFlags MailFlag = 0 |
	FlagSeen |
	FlagAnswered |
	FlagFlagged |
	FlagDeleted |
	FlagDraft |
	FlagForwarded |
	FlagMDNSent |
	FlagJunk |
	FlagNotJunk |
	FlagPhishing |
	FlagImportant

var MailFlagMap = map[MailFlag]imap.Flag{
	// System flags
	FlagSeen:     imap.FlagSeen,
	FlagAnswered: imap.FlagAnswered,
	FlagFlagged:  imap.FlagFlagged,
	FlagDeleted:  imap.FlagDeleted,
	FlagDraft:    imap.FlagDraft,
	// Widely used flags
	FlagForwarded: imap.FlagForwarded,
	FlagMDNSent:   imap.FlagMDNSent,
	FlagJunk:      imap.FlagJunk,
	FlagNotJunk:   imap.FlagNotJunk,
	FlagPhishing:  imap.FlagPhishing,
	FlagImportant: imap.FlagImportant,
	// Permanent flags
	FlagWildcard: imap.FlagWildcard,
}

func (f MailFlag) String() string {
	return string(f.Flag())
}
func (f MailFlag) Flag() imap.Flag {
	flag, ok := MailFlagMap[f]
	if ok {
		return flag
	}
	return imap.Flag("unkown")
}

const MailFlagUnknown = imap.Flag("unknown")

func (f MailFlag) Flags() (ff []imap.Flag) {
	for k, v := range MailFlagMap {
		if f&k != 0 {
			ff = append(ff, v)
		}
	}
	return ff
}
