package tgchat

type AutoRecapSendMode int

const (
	AutoRecapSendModePublicly                 AutoRecapSendMode = iota
	AutoRecapSendModeOnlyPrivateSubscriptions                   // Only users who subscribed to the recap will receive it
)

// TelegraphContentLengthLimit defines the maximum content length for Telegraph API (64KB)
const TelegraphContentLengthLimit = 64 * 1024 // 64KB

func (a AutoRecapSendMode) String() string {
	switch a {
	case AutoRecapSendModePublicly:
		return "公开"
	case AutoRecapSendModeOnlyPrivateSubscriptions:
		return "私聊"
	default:
		return "其他"
	}
}
