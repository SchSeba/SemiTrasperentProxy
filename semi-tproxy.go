package main

import (
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"sync"
	"time"

	semitproxy "github.com/SchSeba/SemiTrasperentProxy/semitproxy"
)

var (
	// tcpListener represents the TCP
	// listening socket that will receive
	// TCP connections from semitproxy
	tcpListener net.Listener

	// udpListener represents tje UDP
	// listening socket that will receive
	// UDP packets from semitproxy
	udpListener *net.UDPConn

	machineSourceIPString string
	machineSourceIP       net.IP

	virtualMachineIPString string
	virtualMachineIP       net.IP
)

// main will initialize the semitproxy
// handling application
func main() {
	log.Println("Starting GoLang semitproxy ")
	var err error

	fmt.Printf("Config %s external pod ip addr\n", os.Args[1])
	machineSourceIPString = os.Args[1]
	machineSourceIP = net.ParseIP(machineSourceIPString)

	virtualMachineIPString = os.Args[2]
	virtualMachineIP = net.ParseIP(virtualMachineIPString)

	log.Println("Binding TCP semitproxy listener to 0.0.0.0:9401")
	tcpListener, err = semitproxy.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP("0.0.0.0"), Port: 9401})
	if err != nil {
		log.Fatalf("Encountered error while binding listener: %s", err)
		return
	}

	defer tcpListener.Close()
	go listenTCP()

	// log.Println("Binding UDP semitproxy listener to 0.0.0.0:8080")
	// udpListener, err = semitproxy.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("0.0.0.0"), Port: 8080})
	// if err != nil {
	// 	log.Fatalf("Encountered error while binding UDP listener: %s", err)
	// 	return
	// }

	// defer udpListener.Close()
	// go listenUDP()

	interruptListener := make(chan os.Signal)
	signal.Notify(interruptListener, os.Interrupt)
	<-interruptListener

	log.Println("semitproxy listener closing")
}

// listenUDP runs in a routine to
// accept UDP connections and hand them
// off into their own routines for handling
func listenUDP() {
	for {
		buff := make([]byte, 1024)
		n, srcAddr, dstAddr, err := semitproxy.ReadFromUDP(udpListener, buff)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Temporary() {
				log.Printf("Temporary error while reading data: %s", netErr)
			}

			log.Fatalf("Unrecoverable error while reading data: %s", err)
			return
		}

		log.Printf("Accepting UDP connection from %s with destination of %s", srcAddr.String(), dstAddr.String())
		go handleUDPConn(buff[:n], srcAddr, dstAddr)
	}
}

// listenTCP runs in a routine to
// accept TCP connections and hand them
// off into their own routines for handling
func listenTCP() {
	for {
		conn, err := tcpListener.Accept()
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Temporary() {
				log.Printf("Temporary error while accepting connection: %s", netErr)
			}

			log.Fatalf("Unrecoverable error while accepting connection: %s", err)
			return
		}

		go handleTCPConn(conn)
	}
}

// handleUDPConn will open a connection
// to the original destination pretending
// to be the client. It will when right
// the received data to the remote host
// and wait a few seconds for any possible
// response data
func handleUDPConn(data []byte, srcAddr, dstAddr *net.UDPAddr) {
	log.Printf("Accepting UDP connection from %s with destination of %s", srcAddr, dstAddr)

	if dstAddr.IP.Equal(machineSourceIP) {
		dstAddr = &net.UDPAddr{IP: virtualMachineIP, Port: srcAddr.Port}
		log.Printf("Receive data from external service addr %s pass it to virtual machine addr %s", srcAddr, dstAddr)
	}

	localConn, err := semitproxy.DialUDP("udp", dstAddr, srcAddr)
	if err != nil {
		log.Printf("Failed to connect to original UDP source [%s]: %s", srcAddr.String(), err)
		return
	}
	defer localConn.Close()

	srcLocalAddr := &net.UDPAddr{IP: machineSourceIP, Port: srcAddr.Port}
	remoteConn, err := semitproxy.DialUDP("udp", srcLocalAddr, dstAddr)
	if err != nil {
		log.Printf("Failed to connect to original UDP destination [%s]: %s", dstAddr.String(), err)
		return
	}
	defer remoteConn.Close()

	bytesWritten, err := remoteConn.Write(data)
	if err != nil {
		log.Printf("Encountered error while writing to remote [%s]: %s", remoteConn.RemoteAddr(), err)
		return
	} else if bytesWritten < len(data) {
		log.Printf("Not all bytes [%d < %d] in buffer written to remote [%s]", bytesWritten, len(data), remoteConn.RemoteAddr())
		return
	}

	data = make([]byte, 1024)
	remoteConn.SetReadDeadline(time.Now().Add(1 * time.Second)) // Add deadline to ensure it doesn't block forever
	bytesRead, err := remoteConn.Read(data)
	if err != nil {
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			return
		}

		log.Printf("Encountered error while reading from remote [%s]: %s", remoteConn.RemoteAddr(), err)
		return
	}

	bytesWritten, err = localConn.Write(data)
	if err != nil {
		log.Printf("Encountered error while writing to local [%s]: %s", localConn.RemoteAddr(), err)
		return
	} else if bytesWritten < bytesRead {
		log.Printf("Not all bytes [%d < %d] in buffer written to locoal [%s]", bytesWritten, len(data), remoteConn.RemoteAddr())
		return
	}
}

// handleTCPConn will open a connection
// to the original destination pretending
// to be the client. From there it will setup
// two routines to stream data between the
// connections
func handleTCPConn(conn net.Conn) {
	var remoteConn *net.TCPConn
	var err error

	log.Printf("Accepting TCP connection from %s with destination of %s", conn.RemoteAddr().String(), conn.LocalAddr().String())
	if machineSourceIP.Equal(conn.LocalAddr().(*net.TCPAddr).IP) || conn.LocalAddr().(*net.TCPAddr).IP.String() == "127.0.0.1" {
		remoteConn, err = conn.(*semitproxy.Conn).DialOriginalDestination(false, &machineSourceIP, &virtualMachineIP)
	} else {
		remoteConn, err = conn.(*semitproxy.Conn).DialOriginalDestination(true, &machineSourceIP, &virtualMachineIP)
	}

	if err != nil {
		log.Printf("Failed to connect to original destination [%s]: %s", conn.LocalAddr().String(), err)
	} else {
		defer remoteConn.Close()
		defer conn.Close()
	}

	var streamWait sync.WaitGroup
	streamWait.Add(2)

	streamConn := func(dst io.Writer, src io.Reader) {
		io.Copy(dst, src)
		streamWait.Done()
	}

	go streamConn(remoteConn, conn)
	go streamConn(conn, remoteConn)

	streamWait.Wait()
}
