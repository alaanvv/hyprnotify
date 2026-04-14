package internal

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
)

const (
	FDN_PATH            = "/org/freedesktop/Notifications"
	FDN_IFAC            = "org.freedesktop.Notifications"
	FDN_NAME            = "org.freedesktop.Notifications"
	INTROSPECTABLE_IFAC = "org.freedesktop.DBus.Introspectable"

	FDN_SPEC_VERSION = "1.2"
	MAX_UINT32       = ^uint32(0)
)

const DBUS_XML = `<node name="` + FDN_PATH + `">
  <interface name="` + FDN_IFAC + `">

      <method name="GetCapabilities">
          <arg direction="out" name="capabilities"    type="as" />
      </method>

      <method name="Notify">
          <arg direction="in"  name="app_name"        type="s"/>
          <arg direction="in"  name="replaces_id"     type="u"/>
          <arg direction="in"  name="app_icon"        type="s"/>
          <arg direction="in"  name="summary"         type="s"/>
          <arg direction="in"  name="body"            type="s"/>
          <arg direction="in"  name="actions"         type="as"/>
          <arg direction="in"  name="hints"           type="a{sv}"/>
          <arg direction="in"  name="expire_timeout"  type="i"/>
          <arg direction="out" name="id"              type="u"/>
      </method> 

      <method name="GetServerInformation">
          <arg direction="out" name="name"            type="s"/>
          <arg direction="out" name="vendor"          type="s"/>
          <arg direction="out" name="version"         type="s"/>
          <arg direction="out" name="spec_version"    type="s"/>
      </method>

      <method name="CloseNotification">
          <arg direction="in"  name="id"              type="u"/>
      </method>

     <signal name="NotificationClosed">
          <arg name="id"         type="u"/>
          <arg name="reason"     type="u"/>
      </signal>

      <signal name="ActionInvoked">
          <arg name="id"         type="u"/>
          <arg name="action_key" type="s"/>
      </signal>
  </interface>
` + introspect.IntrospectDataString + `
</node>`

var (
	conn                        *dbus.Conn
	hyprsock                    HyprConn
	ongoing_notifications       map[uint32]chan uint32 = make(map[uint32]chan uint32)
	current_id                  uint32                 = 0
	notification_padding_regexp *regexp.Regexp         = regexp.MustCompile("^\\s*|(\n)\\s*(.)")
)

type DBusNotify string

func (n DBusNotify) GetCapabilities() ([]string, *dbus.Error) {
	return []string{"body", "actions", "icon-static"}, nil
}

func (n DBusNotify) Notify(
	app_name string,
	replaces_id uint32,
	app_icon string,
	summary string,
	body string,
	actions []string,
	hints map[string]dbus.Variant,
	expire_timeout int32,
) (uint32, *dbus.Error) {

	if _, err := os.Stat("/tmp/dnd"); err == nil {
		return 1, nil
	}

	if replaces_id > 0 {
		n.CloseNotification(replaces_id)
	}
	if current_id == MAX_UINT32 {
		current_id++
	}
	current_id++

	msg := ""

	// Setting min-width, left alignment
	summary = fmt.Sprintf("%-25s", summary)
	summary += "\u205F"

	if body != "" {
		msg = fmt.Sprintf("%s\n%s", summary, body)
	} else {
		msg = summary
	}

	// Using RegExp to add padding for all lines
	msg = notification_padding_regexp.
		ReplaceAllString(
			strings.TrimLeft(msg, "\n"),
			"$1\u205F$2",
		)

	hyprsock.SendNotification(msg)

	// ClosedNotification Signal Stuff
	flag := make(chan uint32, 1)
	ongoing_notifications[current_id] = flag
	go SendCloseSignal(1000, current_id, 1, flag)
	return current_id, nil
}

func (n DBusNotify) CloseNotification(id uint32) *dbus.Error {
	count := 0
	for i := current_id; i >= id; i-- {
		flag, ok := ongoing_notifications[i]
		if ok {
			flag <- 3
		}
		count++
	}

	hyprsock.DismissNotify(count)

	return nil
}

func (n DBusNotify) GetServerInformation() (string, string, string, string, *dbus.Error) {
	return "hyprnotify", "hyprnotify", "0.8.0", FDN_SPEC_VERSION, nil
}

func SendCloseSignal(timeout int32, id uint32, reason uint32, flag chan uint32) {
	d := time.Duration(int64(timeout)) * time.Millisecond

	tick := time.NewTicker(d)
	defer tick.Stop()

	select {
	case <-tick.C:
	case reason = <-flag:
	}
	conn.Emit(
		FDN_PATH,
		"org.freedesktop.Notifications.NotificationClosed",
		id,
		reason,
	)

	delete(ongoing_notifications, id)
}

func InitDBus() {
	var err error
	conn, err = dbus.ConnectSessionBus()
	if err != nil {
		panic(err)
	}
	defer conn.Close()

	GetHyprSocket(&hyprsock)

	n := DBusNotify("hyprnotify")
	conn.Export(n, FDN_PATH, FDN_IFAC)
	conn.Export(introspect.Introspectable(DBUS_XML), FDN_PATH, INTROSPECTABLE_IFAC)

	reply, err := conn.RequestName(FDN_NAME, dbus.NameFlagDoNotQueue)
	if err != nil {
		panic(err)
	}

	if reply != dbus.RequestNameReplyPrimaryOwner {
		fmt.Fprintln(os.Stderr, "name already taken")
		os.Exit(1)
	}
	select {}
}
