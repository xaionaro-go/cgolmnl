package main

/*
#include <stdlib.h>
#include <sys/socket.h>
#include <linux/netlink.h>

#include <linux/netfilter/nfnetlink.h>
#include <linux/netfilter/nfnetlink_log.h>
*/
import "C"

import (
	"fmt"
	mnl "github.com/chamaken/cgolmnl"
	inet "github.com/chamaken/cgolmnl/inet"
	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"os"
	"strconv"
	"syscall"
)

func parse_attr_cb(attr *mnl.Nlattr, data interface{}) (int, syscall.Errno) {
	tb := data.(map[uint16]*mnl.Nlattr)
	attr_type := attr.GetType()

	if err := attr.TypeValid(C.NFULA_MAX); err != nil {
		return mnl.MNL_CB_OK, 0
	}
	switch int(attr_type) {
	case C.NFULA_MARK:
		fallthrough
	case C.NFULA_IFINDEX_INDEV:
		fallthrough
	case C.NFULA_IFINDEX_OUTDEV:
		fallthrough
	case C.NFULA_IFINDEX_PHYSINDEV:
		fallthrough
	case C.NFULA_IFINDEX_PHYSOUTDEV:
		if err := attr.Validate(mnl.MNL_TYPE_U32); err != nil {
			fmt.Fprintf(os.Stderr, "mnl_attr_validate: %s\n", err)
			return mnl.MNL_CB_ERROR, err.(syscall.Errno)
		}
	case C.NFULA_TIMESTAMP:
		if err := attr.Validate2(mnl.MNL_TYPE_UNSPEC, SizeofNfulnlMsgPacketTimestamp); err != nil {
			fmt.Fprintf(os.Stderr, "mnl_attr_validate2: %s\n", err)
			return mnl.MNL_CB_ERROR, err.(syscall.Errno)
		}
	case C.NFULA_HWADDR:
		if err := attr.Validate2(mnl.MNL_TYPE_UNSPEC, SizeofNfulnlMsgPacketHw); err != nil {
			fmt.Fprintf(os.Stderr, "mnl_attr_validate2: %s\n", err)
			return mnl.MNL_CB_ERROR, err.(syscall.Errno)
		}
	case C.NFULA_PREFIX:
		if err := attr.Validate(mnl.MNL_TYPE_NUL_STRING); err != nil {
			fmt.Fprintf(os.Stderr, "mnl_attr_validate: %s\n", err)
			return mnl.MNL_CB_ERROR, err.(syscall.Errno)
		}
	case C.NFULA_PAYLOAD:
		// do something
	}
	tb[attr_type] = attr
	return mnl.MNL_CB_OK, 0
}

func log_cb(nlh *mnl.Nlmsg, data interface{}) (int, syscall.Errno) {
	var ph *NfulnlMsgPacketHdr
	var prefix string
	var mark uint32
	tb := make(map[uint16]*mnl.Nlattr, C.NFULA_MAX+1)

	nfg := (*Nfgenmsg)(nlh.Payload())
	nlh.Parse(SizeofNfgenmsg, parse_attr_cb, tb)
	if tb[C.NFULA_PACKET_HDR] != nil {
		ph = (*NfulnlMsgPacketHdr)(tb[C.NFULA_PACKET_HDR].Payload())
	}
	if tb[C.NFULA_PREFIX] != nil {
		prefix = tb[C.NFULA_PREFIX].Str()
	}
	if tb[C.NFULA_MARK] != nil {
		mark = inet.Ntohl(tb[C.NFULA_MARK].U32())
	}

	fmt.Printf("log received (prefix=\"%s\" hw=0x%04x hook=%d mark=%d)\n",
		prefix, inet.Ntohs(ph.Protocol), ph.Hook, mark)

	if tb[C.NFULA_PAYLOAD] != nil {
		pbuf := tb[C.NFULA_PAYLOAD].PayloadBytes()
		switch nfg.Nfgen_family {
		case C.PF_BRIDGE:
			pkt := layers.Ethernet{}
			if err := pkt.DecodeFromBytes(pbuf, gopacket.NilDecodeFeedback); err != nil {
				fmt.Fprintf(os.Stderr, "Ethernet.DecodeFromBytes: %s\n", err)
				return mnl.MNL_CB_ERROR, syscall.EINVAL
			}
			fmt.Printf("payload src: %v, dst: %v, ether type: %v\n",
				pkt.SrcMAC, pkt.DstMAC, pkt.EthernetType)

		case C.PF_INET:
			pkt := layers.IPv4{}
			if err := pkt.DecodeFromBytes(pbuf, gopacket.NilDecodeFeedback); err != nil {
				fmt.Fprintf(os.Stderr, "IPv4.DecodeFromBytes: %s\n", err)
				return mnl.MNL_CB_ERROR, syscall.EINVAL
			}
			fmt.Printf("payload src: %v, dst: %v, protocol: %v\n",
				pkt.SrcIP, pkt.DstIP, pkt.Protocol)

		case C.PF_INET6:
			pkt := layers.IPv6{}
			if err := pkt.DecodeFromBytes(pbuf, gopacket.NilDecodeFeedback); err != nil {
				fmt.Fprintf(os.Stderr, "IPv6.DecodeFromBytes: %s\n", err)
				return mnl.MNL_CB_ERROR, syscall.EINVAL
			}
			fmt.Printf("payload src: %v, dst: %v, next header: %v\n",
				pkt.SrcIP, pkt.DstIP, pkt.NextHeader)
		}
	}

	return mnl.MNL_CB_OK, 0
}

func nflog_build_cfg_pf_request(buf []byte, command uint8) *mnl.Nlmsg {
	nlh, err := mnl.NewNlmsgBytes(buf)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nlmsg_put_header: %s\n", err)
		os.Exit(C.EXIT_FAILURE)
	}

	nlh.Type = (C.NFNL_SUBSYS_ULOG << 8) | C.NFULNL_MSG_CONFIG
	nlh.Flags = C.NLM_F_REQUEST

	nfg := (*Nfgenmsg)(nlh.PutExtraHeader(SizeofNfgenmsg))
	nfg.Nfgen_family = C.AF_INET
	nfg.Version = C.NFNETLINK_V0

	cmd := &NfulnlMsgConfigCmd{Command: command}
	nlh.PutPtr(C.NFULA_CFG_CMD, cmd)

	return nlh
}

func nflog_build_cfg_request(buf []byte, command uint8, qnum int) *mnl.Nlmsg {
	nlh, err := mnl.NewNlmsgBytes(buf)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nlmsg_put_header: %s\n", err)
		os.Exit(C.EXIT_FAILURE)
	}

	nlh.Type = (C.NFNL_SUBSYS_ULOG << 8) | C.NFULNL_MSG_CONFIG
	nlh.Flags = C.NLM_F_REQUEST

	nfg := (*Nfgenmsg)(nlh.PutExtraHeader(SizeofNfgenmsg))
	nfg.Nfgen_family = C.AF_INET
	nfg.Version = C.NFNETLINK_V0
	nfg.Res_id = inet.Htons(uint16(qnum))

	cmd := &NfulnlMsgConfigCmd{Command: command}
	nlh.PutPtr(C.NFULA_CFG_CMD, cmd)

	return nlh
}

func nflog_build_cfg_params(buf []byte, copy_mode uint8, copy_range, qnum int) *mnl.Nlmsg {
	nlh, err := mnl.NewNlmsgBytes(buf)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nlmsg_put_header: %s\n", err)
		os.Exit(C.EXIT_FAILURE)
	}

	nlh.Type = (C.NFNL_SUBSYS_ULOG << 8) | C.NFULNL_MSG_CONFIG
	nlh.Flags = C.NLM_F_REQUEST

	nfg := (*Nfgenmsg)(nlh.PutExtraHeader(SizeofNfgenmsg))
	nfg.Nfgen_family = C.AF_UNSPEC
	nfg.Version = C.NFNETLINK_V0
	nfg.Res_id = inet.Htons(uint16(qnum))

	params := &NfulnlMsgConfigMode{Range: inet.Htonl(uint32(copy_range)), Mode: copy_mode}
	nlh.PutPtr(C.NFULA_CFG_MODE, params)

	return nlh
}

func main() {
	if len(os.Args) != 2 {
		fmt.Printf("Usage: %s [queue_num]\n", os.Args[0])
		os.Exit(C.EXIT_FAILURE)
	}
	qnum, _ := strconv.Atoi(os.Args[1])

	nl, err := mnl.NewSocket(C.NETLINK_NETFILTER)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mnl_socket_open: %s\n", err)
		os.Exit(C.EXIT_FAILURE)
	}
	defer nl.Close()

	if err = nl.Bind(0, mnl.MNL_SOCKET_AUTOPID); err != nil {
		fmt.Fprintf(os.Stderr, "mnl_socket_bind: %s\n", err)
		os.Exit(C.EXIT_FAILURE)
	}
	portid := nl.Portid()

	buf := make([]byte, mnl.MNL_SOCKET_BUFFER_SIZE)

	nlh := nflog_build_cfg_pf_request(buf, C.NFULNL_CFG_CMD_PF_UNBIND)
	if _, err := nl.SendNlmsg(nlh); err != nil {
		fmt.Fprintf(os.Stderr, "mnl_socket_sendto: %s\n", err)
		os.Exit(C.EXIT_FAILURE)
	}

	nlh = nflog_build_cfg_pf_request(buf, C.NFULNL_CFG_CMD_PF_BIND)
	if _, err := nl.SendNlmsg(nlh); err != nil {
		fmt.Fprintf(os.Stderr, "mnl_socket_sendto: %s\n", err)
		os.Exit(C.EXIT_FAILURE)
	}

	nlh = nflog_build_cfg_request(buf, C.NFULNL_CFG_CMD_BIND, qnum)
	if _, err := nl.SendNlmsg(nlh); err != nil {
		fmt.Fprintf(os.Stderr, "mnl_socket_sendto: %s\n", err)
		os.Exit(C.EXIT_FAILURE)
	}

	nlh = nflog_build_cfg_params(buf, C.NFULNL_COPY_PACKET, 0xFFFF, qnum)

	if _, err := nl.SendNlmsg(nlh); err != nil {
		fmt.Fprintf(os.Stderr, "mnl_socket_sendto: %s\n", err)
		os.Exit(C.EXIT_FAILURE)
	}

	ret := mnl.MNL_CB_OK
	for ret >= mnl.MNL_CB_STOP {
		nrcv, err := nl.Recvfrom(buf)
		if err != nil {
			fmt.Fprintf(os.Stderr, "mnl_socket_recvfrom: %s\n", err)
			os.Exit(C.EXIT_FAILURE)
		}
		ret, err = mnl.CbRun(buf[:nrcv], 0, portid, log_cb, nil)
	}

	if ret < mnl.MNL_CB_STOP {
		fmt.Fprintf(os.Stderr, "mnl_cb_run: %s\n", err)
		os.Exit(C.EXIT_FAILURE)
	}
}
