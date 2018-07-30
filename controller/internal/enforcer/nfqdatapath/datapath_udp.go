package nfqdatapath

// Go libraries
import (
	"errors"
	"fmt"
	"github.com/google/gopacket/layers"
	"net"
	"strconv"
	"strings"

	"go.uber.org/zap"
	"go.aporeto.io/trireme-lib/policy"

	"go.aporeto.io/trireme-lib/collector"
	"go.aporeto.io/trireme-lib/controller/constants"
	"go.aporeto.io/trireme-lib/controller/internal/enforcer/constants"
	"go.aporeto.io/trireme-lib/controller/pkg/connection"
	"go.aporeto.io/trireme-lib/controller/pkg/packet"
	"go.aporeto.io/trireme-lib/controller/pkg/pucontext"
	"go.aporeto.io/trireme-lib/controller/pkg/tokens"
)

// ProcessNetworkUDPPacket processes packets arriving from network and are destined to the application
func (d *Datapath) ProcessNetworkUDPPacket(p *packet.Packet) (err error) {

	if d.packetLogs {
		zap.L().Debug("Processing network packet ",
			zap.String("flow", p.L4FlowHash()),
		)

		defer zap.L().Debug("Finished Processing network packet ",
			zap.String("flow", p.L4FlowHash()),
			zap.Error(err),
		)
	}

	if p.SourcePort == 53 {
		d.processDNSTraffic(p, false)
		fmt.Println("Saw DNS response traffic. Letting it through")
		return nil
	}

	var conn *connection.UDPConnection
	udpPacketType := p.GetUDPType()

	zap.L().Debug("Got packet of type:", zap.Reflect("Type", udpPacketType), zap.Reflect("Len", len(p.Buffer)))
	switch udpPacketType {
	case packet.UDPSynMask:
		zap.L().Debug("Received Syn Network packet")
		conn, err = d.netSynUDPRetrieveState(p)
		if err != nil {
			if d.packetLogs {
				zap.L().Debug("Packet rejected",
					zap.String("flow", p.L4FlowHash()),
					zap.Error(err),
				)
			}
			return err
		}

		if conn == nil {
			zap.L().Debug("Conn should never be nil")
			return fmt.Errorf("Unable to create new connection")
		}
		conn.SetState(connection.UDPSynReceived)

	case packet.UDPSynAckMask:
		zap.L().Debug("Received Syn Ack Network packet")
		conn, err = d.netSynAckUDPRetrieveState(p)
		if err != nil {
			if d.packetLogs {
				zap.L().Debug("Syn ack Packet Rejected/ignored",
					zap.String("flow", p.L4FlowHash()),
				)
			}
			// flush the packetQueue on errors.
			if conn != nil {
				zap.L().Debug("Dropping packets ")
				conn.DropPackets()
			}
			return err
		}

	case packet.UDPAckMask:
		zap.L().Debug("Received udp Network packet")
		conn, err = d.netUDPAckRetrieveState(p)
		if err != nil {
			if d.packetLogs {
				zap.L().Debug("Packet rejected",
					zap.String("flow", p.L4FlowHash()),
					zap.Error(err),
				)
			}
			return err
		}
	default:
		// Decrypt the packet and deliver to the application.
		zap.L().Debug("Not an aporeto handshake packet, Check for encryption", zap.String("flow", p.L4FlowHash()))
		conn, err = d.netUDPAckRetrieveState(p)
		if err != nil {
			if d.packetLogs {
				zap.L().Debug("No connection found for the flow, Dropping it",
					zap.String("flow", p.L4FlowHash()),
					zap.Error(err),
				)
			}
			return err
		}

		// decrypt the packet
		if d.service != nil {
			if !d.service.PostProcessUDPNetPacket(p, nil, nil, conn.Context, conn) {
				p.Print(packet.PacketFailureService)
				return errors.New("post service processing failed for network packet")
			}
		}
		zap.L().Debug("Delivering packet to application")
		return nil
	}

	p.Print(packet.PacketStageIncoming)

	if d.service != nil {
		if !d.service.PreProcessUDPNetPacket(p, conn.Context, conn) {
			p.Print(packet.PacketFailureService)
			return errors.New("pre service processing failed for network packet")
		}
	}

	// handle handshake packets and do not deliver to application.
	err = d.processNetUDPPacket(p, conn.Context, conn)
	if err != nil {
		if d.packetLogs {
			zap.L().Debug("Rejecting packet ",
				zap.String("flow", p.L4FlowHash()),
				zap.Error(err),
			)
		}
		return fmt.Errorf("packet processing failed for network packet: %s", err)
	}
	// handshake packets are not to be delivered to application.
	return fmt.Errorf("Drop net hanshake packets (udp)")
}

func (d *Datapath) netSynUDPRetrieveState(p *packet.Packet) (*connection.UDPConnection, error) {

	context, err := d.contextFromIP(false, p.DestinationAddress.String(), p.Mark, p.DestinationPort, packet.IPProtocolUDP)
	if err != nil {
		zap.L().Debug("Received Packets from unenforcerd process")
		return nil, err
	}

	return connection.NewUDPConnection(context, d.udpSocketWriter), nil
}

func (d *Datapath) netSynAckUDPRetrieveState(p *packet.Packet) (*connection.UDPConnection, error) {

	conn, err := d.udpSourcePortConnectionCache.GetReset(p.SourcePortHash(packet.PacketTypeNetwork), 0)
	if err != nil {
		if d.packetLogs {
			zap.L().Debug("No connection for udp SynAck packet, ignore it",
				zap.String("flow", p.L4FlowHash()),
			)
		}
		// ignore the syn ack packet.
		return nil, nil
	}

	return conn.(*connection.UDPConnection), nil
}

func (d *Datapath) netUDPAckRetrieveState(p *packet.Packet) (*connection.UDPConnection, error) {

	hash := p.L4FlowHash()
	conn, err := d.udpNetReplyConnectionTracker.GetReset(hash, 0)
	if err != nil {
		conn, err = d.udpNetOrigConnectionTracker.GetReset(hash, 0)
		if err != nil {
			return nil, fmt.Errorf("net state not found: %s", err)
		}
	}
	return conn.(*connection.UDPConnection), nil
}

// processNetUDPPacket processes a network UDP packet and dispatches it to different methods based on the flags
func (d *Datapath) processNetUDPPacket(udpPacket *packet.Packet, context *pucontext.PUContext, conn *connection.UDPConnection) (err error) {

	if conn == nil {
		return nil
	}

	udpPacketType := udpPacket.GetUDPType()
	// Update connection state in the internal state machine tracker
	switch udpPacketType {

	case packet.UDPSynMask:
		zap.L().Debug("Prcessing net udp syn packet")
		action, claims, err := d.processNetworkUDPSynPacket(context, conn, udpPacket)
		if err != nil {
			return err
		}

		if d.service != nil {
			if !d.service.PostProcessUDPNetPacket(udpPacket, action, claims, conn.Context, conn) {
				udpPacket.Print(packet.PacketFailureService)
				return errors.New("post service processing failed for network packet")
			}
		}
		// setup Encryption based on action and claims.
		zap.L().Debug("Sending UDP syn ack packet")
		err = d.sendUDPSynAckPacket(udpPacket, context, conn)
		if err != nil {
			return err
		}

	case packet.UDPAckMask:
		zap.L().Debug("Processing network Ack Packet")
		action, claims, err := d.processNetworkUDPAckPacket(udpPacket, context, conn)
		if err != nil {
			zap.L().Error("Error during authorization", zap.Error(err))
			return err
		}
		// ack is processed, mark connmark rule and let other packets go through.
		if d.service != nil {
			if !d.service.PostProcessUDPNetPacket(udpPacket, action, claims, conn.Context, conn) {
				udpPacket.Print(packet.PacketFailureService)
				return errors.New("post service processing failed for network packet")
			}
		}
		conn.SetState(connection.UDPAckProcessed)
		// we have drop the handshake packet after succesfull completion of the handshake.
		return fmt.Errorf("Drop udp ack (net) handshake packet as it is not intended for the application")

	case packet.UDPSynAckMask:
		// action, claims
		zap.L().Debug("Processing net udp syn ack packet")
		action, claims, err := d.processNetworkUDPSynAckPacket(udpPacket, context, conn)
		if err != nil {
			zap.L().Error("UDP Syn ack failed with", zap.Error(err))
			return err
		}

		if d.service != nil {
			if !d.service.PostProcessUDPNetPacket(udpPacket, action, claims, conn.Context, conn) {
				udpPacket.Print(packet.PacketFailureService)
				return errors.New("post service processing failed for network packet")
			}
		}
		zap.L().Debug("Sending udp ack packet - 3rd packet")
		err = d.sendUDPAckPacket(udpPacket, context, conn)
		if err != nil {
			zap.L().Error("Unable to send udp Syn ack failed", zap.Error(err))
			return err
		}
	}
	return nil
}

type dnsl struct {
}

func (d *dnsl) SetTruncated() {

}

var dnsmap = map[uint16]*pucontext.PUContext{}

func(d *Datapath) processDNSTraffic(p *packet.Packet, request bool) {
	var dns layers.DNS
	var dn dnsl

	dns.DecodeFromBytes(p.GetUDPData(), &dn)

	if request == true {
		context, err := d.contextFromIP(true, p.SourceAddress.String(), p.Mark, p.SourcePort, packet.IPProtocolUDP)
		if err != nil {
			zap.L().Error("can not find context")
		}

		dnsmap[p.SourcePort] = context
	} else {
		context := dnsmap[p.DestinationPort]
		if context == nil {
			zap.L().Error("error")
			return
		}

		fmt.Println(&context.DNSACLs)
		
		for _, t := range dns.Answers {
			if t.DataLength == 4 {
				fmt.Printf("looking for %s in %v\n", t.Name, context)
				context.DNSACLs.List()
				if val, err := context.DNSACLs.Get(fmt.Sprintf("%s", t.Name)); err == nil {
					fmt.Printf("found %s", t.Name)
					for _, port := range strings.Split(val.(string), ",") {
						fmt.Println("MC: ", port)
						if err = context.ApplicationACLs.AddRule(policy.IPRule{
							Address:  fmt.Sprintf("%s", t.IP),
							Port:     port,
							Protocol: "TCP",
							Policy: &policy.FlowPolicy{
								Action:        policy.Accept,
								ObserveAction: policy.ObserveNone,
								ServiceID:     "default",
								PolicyID:      "default",
							},
						}); err != nil {
							fmt.Println("error insert rule ", err)
						}
					}
				}
			}
		}
	}
}

// ProcessApplicationUDPPacket processes packets arriving from an application and are destined to the network
func (d *Datapath) ProcessApplicationUDPPacket(p *packet.Packet) (err error) {

	zap.L().Debug("Processing application UDP packet ",
		zap.String("flow", p.L4FlowHash()),
	)

	defer zap.L().Debug("Finished Processing UDP application packet ",
		zap.String("flow", p.L4FlowHash()),
		zap.Error(err),
	)

	if p.DestinationPort == 53 {
		d.processDNSTraffic(p, true)
		fmt.Println("DNS request. Letting it through")
		return nil
	}

	var conn *connection.UDPConnection
	conn, err = d.appUDPRetrieveState(p)
	if err != nil {
		zap.L().Debug("Connection not found ?", zap.Error(err))
		return fmt.Errorf("Received packet from unenforced process: %s", err)
	}

	// queue packets if connection is still unauthorized.
	if conn.GetState() != connection.UDPAckProcessed {
		zap.L().Debug("Packets are being queueed", zap.String("flow", p.L4FlowHash()))
		if err = conn.QueuePackets(p); err != nil {
			return fmt.Errorf("Unable to queue packets:%s", err)
		}
	}

	switch conn.GetState() {

	case connection.UDPSynStart:
		// connection not authorized yet. queue the packets and start handshake.
		zap.L().Debug("Sending out Application UDP Syn Packet with options", zap.String("flow", p.L4FlowHash()))
		err = d.processApplicationUDPSynPacket(p, conn.Context, conn)

		if err != nil {
			return fmt.Errorf("Unable to send UDP Syn packet: %s", err)
		}

	case connection.UDPAckProcessed:
		// check for encryption and do the needful.
		if d.service != nil {
			// PostProcessServiceInterface
			if !d.service.PostProcessUDPAppPacket(p, nil, conn.Context, conn) {
				p.Print(packet.PacketFailureService)
				return errors.New("Encryption failed for application packet")
			}
		}
		// Set verdict to 1.
		zap.L().Debug("Ack Processed, write on the app socket")
		return nil
	}
	// if not in the above two states, packets are queued. NFQ can drop them, as they are already
	// in connection packet queue.
	zap.L().Debug("Dropping packet by setting verdict to 0", zap.String("flow", p.L4FlowHash()))
	return fmt.Errorf("Packets cloned. Dropping the original packet")
}

func (d *Datapath) appUDPRetrieveState(p *packet.Packet) (*connection.UDPConnection, error) {

	hash := p.L4FlowHash()

	if conn, err := d.udpAppReplyConnectionTracker.GetReset(hash, 0); err == nil {
		return conn.(*connection.UDPConnection), nil
	}

	if conn, err := d.udpAppOrigConnectionTracker.GetReset(hash, 0); err == nil {
		return conn.(*connection.UDPConnection), nil
	}

	context, err := d.contextFromIP(true, p.SourceAddress.String(), p.Mark, p.SourcePort, packet.IPProtocolUDP)
	if err != nil {
		return nil, errors.New("No context in app processing")
	}

	return connection.NewUDPConnection(context, d.udpSocketWriter), nil
}

// processApplicationUDPSynPacket processes a single Syn Packet
func (d *Datapath) processApplicationUDPSynPacket(udpPacket *packet.Packet, context *pucontext.PUContext, conn *connection.UDPConnection) (err error) {
	// do some pre processing.
	if d.service != nil {
		// PreProcessServiceInterface
		if !d.service.PreProcessUDPAppPacket(udpPacket, conn.Context, conn, packet.UDPSynMask) {
			udpPacket.Print(packet.PacketFailureService)
			return errors.New("pre service processing failed for UDP application packet")
		}
	}

	udpOptions := d.CreateUDPAuthMarker(packet.UDPSynMask)
	udpData, err := d.tokenAccessor.CreateSynPacketToken(context, &conn.Auth)

	if err != nil {
		return err
	}

	newPacket, err := d.clonePacketHeaders(udpPacket)
	if err != nil {
		return fmt.Errorf("Unable to clone packet: %s", err)
	}
	// Attach the UDP data and token
	newPacket.UDPTokenAttach(udpOptions, udpData)

	// send packet
	err = d.udpSocketWriter.WriteSocket(newPacket.Buffer)
	if err != nil {
		zap.L().Debug("Unable to send syn token on raw socket", zap.Error(err))
	}

	// Set the state indicating that we send out a Syn packet
	conn.SetState(connection.UDPSynSend)

	// Poplate the caches to track the connection
	hash := udpPacket.L4FlowHash()
	d.udpAppOrigConnectionTracker.AddOrUpdate(hash, conn)
	d.udpSourcePortConnectionCache.AddOrUpdate(newPacket.SourcePortHash(packet.PacketTypeApplication), conn)
	d.udpNatConnectionTracker.AddOrUpdate(newPacket.SourcePortHash(packet.PacketTypeApplication), newPacket.SourcePortHash(packet.PacketTypeNetwork))
	// Attach the tags to the packet and accept the packet

	if d.service != nil {
		// PostProcessServiceInterface
		if !d.service.PostProcessUDPAppPacket(newPacket, nil, conn.Context, conn) {
			newPacket.Print(packet.PacketFailureService)
			return errors.New("post service processing failed for application packet")
		}
	}

	return nil

}

func (d *Datapath) clonePacketHeaders(p *packet.Packet) (*packet.Packet, error) {
	// copy the ip and udp headers.
	newPacket := make([]byte, packet.UDPDataPos)
	p.FixupIPHdrOnDataModify(p.IPTotalLength, packet.UDPDataPos)
	_ = copy(newPacket, p.Buffer[:packet.UDPDataPos])

	return packet.New(packet.PacketTypeApplication, newPacket, p.Mark)
}

// CreateUDPAuthMarker creates a UDP auth marker.
func (d *Datapath) CreateUDPAuthMarker(packetType uint8) []byte {

	// Every UDP control packet has a 20 byte packet signature. The
	// first 2 bytes represent the following control information.
	// Byte 0 : Bits 0,1 are reserved fields.
	//          Bits 2,3,4 represent version information.
	//          Bits 5, 6 represent udp packet type,
	//          Bit 7 represents encryption. (currently unused).
	// Byte 1: reserved for future use.
	// Bytes [2:20]: Packet signature.

	marker := make([]byte, packet.UDPSignatureLen)
	// ignore version info as of now.
	marker[0] |= packetType // byte 0
	marker[1] = 0           // byte 1
	// byte 2 - 19
	copy(marker[2:], []byte(packet.UDPAuthMarker))

	return marker
}

// processApplicationSynAckPacket processes a UDP SynAck packet
func (d *Datapath) sendUDPSynAckPacket(udpPacket *packet.Packet, context *pucontext.PUContext, conn *connection.UDPConnection) (err error) {

	// check for service and configure encryption based on
	// policy that was resolved on network syn.
	if d.service != nil {
		// PreProcessServiceInterface
		if !d.service.PreProcessUDPAppPacket(udpPacket, context, conn, packet.UDPSynAckMask) {
			udpPacket.Print(packet.PacketFailureService)
			return errors.New("pre service processing failed for application packet")
		}
	}

	// Create UDP Option
	udpOptions := d.CreateUDPAuthMarker(packet.UDPSynAckMask)

	udpData, err := d.tokenAccessor.CreateSynAckPacketToken(context, &conn.Auth)

	if err != nil {
		return err
	}

	udpPacket.CreateReverseFlowPacket(udpPacket.SourceAddress, udpPacket.SourcePort)
	// Set the state for future reference
	conn.SetState(connection.UDPSynAckSent)

	// Attach the UDP data and token
	udpPacket.UDPTokenAttach(udpOptions, udpData)

	// Setup ConnMark for encryption.
	if d.service != nil {
		// PostProcessServiceInterface
		if !d.service.PostProcessUDPAppPacket(udpPacket, nil, context, conn) {
			udpPacket.Print(packet.PacketFailureService)
			return errors.New("post service processing failed for application packet")
		}
	}

	// send packet
	err = d.udpSocketWriter.WriteSocket(udpPacket.Buffer)
	if err != nil {
		zap.L().Debug("Unable to send synack token on raw socket", zap.Error(err))
	}

	return nil
}

func (d *Datapath) sendUDPAckPacket(udpPacket *packet.Packet, context *pucontext.PUContext, conn *connection.UDPConnection) (err error) {

	// Create UDP Option
	zap.L().Debug("Sending UDP Ack packet", zap.String("flow", udpPacket.L4ReverseFlowHash()))
	udpOptions := d.CreateUDPAuthMarker(packet.UDPAckMask)

	udpData, err := d.tokenAccessor.CreateAckPacketToken(context, &conn.Auth)

	if err != nil {
		return err
	}

	srcPortHash, err := d.udpNatConnectionTracker.GetReset(udpPacket.SourcePortHash(packet.PacketTypeNetwork), 0)
	if err != nil {
		return fmt.Errorf("error getting actual destination")
	}

	destIPPort := srcPortHash.(string)
	destIP := strings.Split(destIPPort, ":")[0]
	destPort, err := (strconv.Atoi(strings.Split(destIPPort, ":")[1]))
	if err != nil {
		return fmt.Errorf("Unable to get dest port from cache")
	}

	udpPacket.CreateReverseFlowPacket(net.ParseIP(destIP), uint16(destPort))

	// Attach the UDP data and token
	udpPacket.UDPTokenAttach(udpOptions, udpData)

	// send packet
	err = d.udpSocketWriter.WriteSocket(udpPacket.Buffer)
	if err != nil {
		zap.L().Debug("Unable to send ack token on raw socket", zap.Error(err))
	}

	conn.SetState(connection.UDPAckProcessed)

	if !conn.ServiceConnection {
		zap.L().Debug("Plumbing the conntrack (app) rule for flow", zap.String("flow", udpPacket.L4FlowHash()))

		if err = d.conntrackHdl.ConntrackTableUpdateMark(
			destIP,
			udpPacket.SourceAddress.String(),
			udpPacket.IPProto,
			uint16(destPort),
			udpPacket.SourcePort,
			constants.DefaultConnMark,
		); err != nil {
			zap.L().Error("Failed to update conntrack table for flow",
				zap.String("context", string(conn.Auth.LocalContext)),
				zap.String("app-conn", udpPacket.L4ReverseFlowHash()),
				zap.String("state", fmt.Sprintf("%d", conn.GetState())),
				zap.Error(err),
			)
		}
	}

	// Transmit Queued Packets.
	for _, udpPacket := range conn.PacketQueue {
		// check for Encryption.
		if d.service != nil {
			// PostProcessServiceInterface
			if !d.service.PostProcessUDPAppPacket(udpPacket, nil, conn.Context, conn) {
				udpPacket.Print(packet.PacketFailureService)
				return errors.New("Encryption failed for queued application packet")
			}
		}
		zap.L().Debug("Transmitting Queued UDP packets")
		err = d.udpSocketWriter.WriteSocket(udpPacket.Buffer)
		if err != nil {
			zap.L().Error("Unable to transmit Queued UDP packets", zap.Error(err))
		}
	}

	return err
}

// processNetworkUDPSynPacket processes a syn packet arriving from the network
func (d *Datapath) processNetworkUDPSynPacket(context *pucontext.PUContext, conn *connection.UDPConnection, udpPacket *packet.Packet) (action interface{}, claims *tokens.ConnectionClaims, err error) {

	// what about external services ??????
	claims, err = d.tokenAccessor.ParsePacketToken(&conn.Auth, udpPacket.ReadUDPToken())
	if err != nil {
		d.reportUDPRejectedFlow(udpPacket, conn, collector.DefaultEndPoint, context.ManagementID(), context, collector.InvalidToken, nil, nil)
		return nil, nil, fmt.Errorf("UDP Syn packet dropped because of invalid token: %s", err)
	}

	// if there are no claims we must drop the connection and we drop the Syn
	// packet. The source will retry but we have no state to maintain here.
	if claims == nil {
		d.reportUDPRejectedFlow(udpPacket, conn, collector.DefaultEndPoint, context.ManagementID(), context, collector.InvalidToken, nil, nil)
		return nil, nil, errors.New("UDP Syn packet dropped because of no claims")
	}

	// Why is this required. Take a look.
	txLabel, _ := claims.T.Get(enforcerconstants.TransmitterLabel)

	// Add the port as a label with an @ prefix. These labels are invalid otherwise
	// If all policies are restricted by port numbers this will allow port-specific policies
	claims.T.AppendKeyValue(enforcerconstants.PortNumberLabelString, strconv.Itoa(int(udpPacket.DestinationPort)))

	report, pkt := context.SearchRcvRules(claims.T)
	if pkt.Action.Rejected() {
		// txLabel
		d.reportUDPRejectedFlow(udpPacket, conn, txLabel, context.ManagementID(), context, collector.PolicyDrop, report, pkt)
		return nil, nil, fmt.Errorf("connection rejected because of policy: %s", claims.T.String())
	}

	hash := udpPacket.L4FlowHash()
	conn.SetState(connection.UDPSynReceived)

	// conntrack
	d.udpNetOrigConnectionTracker.AddOrUpdate(hash, conn)
	d.udpAppReplyConnectionTracker.AddOrUpdate(udpPacket.L4ReverseFlowHash(), conn)

	// Record actions
	conn.ReportFlowPolicy = report
	conn.PacketFlowPolicy = pkt

	return pkt, claims, nil
}

func (d *Datapath) processNetworkUDPSynAckPacket(udpPacket *packet.Packet, context *pucontext.PUContext, conn *connection.UDPConnection) (action interface{}, claims *tokens.ConnectionClaims, err error) {

	// Packets that have authorization information go through the auth path
	// Decode the JWT token using the context key
	claims, err = d.tokenAccessor.ParsePacketToken(&conn.Auth, udpPacket.ReadUDPToken())
	if err != nil {
		d.reportUDPRejectedFlow(udpPacket, nil, collector.DefaultEndPoint, context.ManagementID(), context, collector.MissingToken, nil, nil)
		return nil, nil, fmt.Errorf("SynAck packet dropped because of bad claims: %s", err)
	}

	if claims == nil {
		d.reportUDPRejectedFlow(udpPacket, nil, collector.DefaultEndPoint, context.ManagementID(), context, collector.MissingToken, nil, nil)
		return nil, nil, errors.New("SynAck packet dropped because of no claims")
	}

	report, pkt := context.SearchTxtRules(claims.T, !d.mutualAuthorization)
	if pkt.Action.Rejected() {
		d.reportUDPRejectedFlow(udpPacket, conn, context.ManagementID(), conn.Auth.RemoteContextID, context, collector.PolicyDrop, report, pkt)
		return nil, nil, fmt.Errorf("dropping because of reject rule on transmitter: %s", claims.T.String())
	}

	conn.SetState(connection.UDPSynAckReceived)
	// conntrack
	d.udpNetReplyConnectionTracker.AddOrUpdate(udpPacket.L4FlowHash(), conn)

	return pkt, claims, nil
}

func (d *Datapath) processNetworkUDPAckPacket(udpPacket *packet.Packet, context *pucontext.PUContext, conn *connection.UDPConnection) (action interface{}, claims *tokens.ConnectionClaims, err error) {

	_, err = d.tokenAccessor.ParseAckToken(&conn.Auth, udpPacket.ReadUDPToken())
	if err != nil {
		d.reportUDPRejectedFlow(udpPacket, conn, conn.Auth.RemoteContextID, context.ManagementID(), context, collector.PolicyDrop, conn.ReportFlowPolicy, conn.PacketFlowPolicy)
		return nil, nil, fmt.Errorf("ack packet dropped because signature validation failed: %s", err)
	}

	conn.SetState(connection.UDPAckReceived)

	if !conn.ServiceConnection {
		zap.L().Debug("Plumb conntrack rule for flow:", zap.String("flow", udpPacket.L4FlowHash()))
		// Plumb connmark rule here.
		if err := d.conntrackHdl.ConntrackTableUpdateMark(
			udpPacket.DestinationAddress.String(),
			udpPacket.SourceAddress.String(),
			udpPacket.IPProto,
			udpPacket.DestinationPort,
			udpPacket.SourcePort,
			constants.DefaultConnMark,
		); err != nil {
			zap.L().Error("Failed to update conntrack table after ack packet")
		}
	}

	d.reportUDPAcceptedFlow(udpPacket, conn, conn.Auth.RemoteContextID, context.ManagementID(), context, conn.ReportFlowPolicy, conn.PacketFlowPolicy)

	return nil, nil, nil
}
