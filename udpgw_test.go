package main

import (
	"bufio"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

func TestUDPGWRelaysIPv4Datagram(t *testing.T) {
	echo, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		b := make([]byte, 2048)
		n, addr, e := echo.ReadFromUDP(b)
		if e == nil {
			_, _ = echo.WriteToUDP(b[:n], addr)
		}
	}()

	srv, err := startUDPGWServer(udpgwServerConfig{Listen: "127.0.0.1:0", MaxClients: 4})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	c, err := net.DialTimeout("tcp", srv.ln.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))

	port := echo.LocalAddr().(*net.UDPAddr).Port
	data := []byte("udpgw-ok")
	payloadLen := 9 + len(data)
	frame := make([]byte, 2+payloadLen)
	binary.LittleEndian.PutUint16(frame[:2], uint16(payloadLen))
	binary.BigEndian.PutUint16(frame[2:4], 1)
	frame[4] = 0
	copy(frame[5:9], []byte{127, 0, 0, 1})
	binary.BigEndian.PutUint16(frame[9:11], uint16(port))
	copy(frame[11:], data)
	if _, err := c.Write(frame); err != nil {
		t.Fatal(err)
	}

	r := bufio.NewReader(c)
	var lb [2]byte
	if _, err := io.ReadFull(r, lb[:]); err != nil {
		t.Fatal(err)
	}
	n := int(binary.LittleEndian.Uint16(lb[:]))
	reply := make([]byte, n)
	if _, err := io.ReadFull(r, reply); err != nil {
		t.Fatal(err)
	}
	if n < 9 || string(reply[9:]) != string(data) {
		t.Fatalf("bad reply n=%d data=%q", n, reply[9:])
	}
}
