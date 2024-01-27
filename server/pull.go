package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v3"
)

var PullServerCache = make(map[string]*PullServer)

// 创建一个 pull server, 是对等连接的服务端, 用于接收客户端的推流

// 创建一个WebRTC配置
var config = webrtc.Configuration{
	ICEServers: []webrtc.ICEServer{},
}

type trackInfo struct {
	trackChan chan []byte
	rtpSender *webrtc.RTPSender
}

type PullServer struct {
	Id         string
	SignalConn *websocket.Conn
	PeerConn   *webrtc.PeerConnection
	trackChan  map[string]*trackInfo // 用于接收客户端的 track 数据

	CloseChan chan struct{}
	ClientMap map[string]*PullServer // 订阅的客户端对象

	detectTimer *time.Ticker // 对方性能检测定时器
}

func NewPullServer() *PullServer {
	channelId, err := uuid.NewUUID()
	if err != nil {
		panic(err)
	}

	_, iceConnectedCtxCancel := context.WithCancel(context.Background())

	peerConnection, err := webrtc.NewPeerConnection(config)
	if err != nil {
		panic(err)
	}

	c := NewSignalConnection(channelId.String(), peerConnection, "server", func(id string) {
		DeletePullServer(id)
	})

	peerConnection.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate != nil {
			candidateBytes, err := json.Marshal(candidate.ToJSON())
			if err != nil {
				panic(err)
			}
			candidateBase64 := base64.StdEncoding.EncodeToString(candidateBytes)
			candidateMessage := BindMessage{
				ChannelId: channelId.String(),
				Role:      "server",
				Type:      "candidate",
				Payload:   candidateBase64,
			}
			err = c.WriteJSON(candidateMessage)
			if err != nil {
				println(err)
			}
		}
	})

	peerConnection.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
		fmt.Printf("Connection State has changed %s \n", connectionState.String())
		if connectionState == webrtc.ICEConnectionStateConnected {
			iceConnectedCtxCancel()
		}
	})
	peerConnection.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		fmt.Printf("Peer Connection State has changed: %s\n", s.String())
		if s == webrtc.PeerConnectionStateFailed {
			fmt.Println("Peer Connection has gone to failed exiting")
			os.Exit(0)
		}
	})

	p := &PullServer{
		Id:         channelId.String(),
		SignalConn: c,
		PeerConn:   peerConnection,
		trackChan:  make(map[string]*trackInfo),
		CloseChan:  make(chan struct{}),
		ClientMap:  make(map[string]*PullServer),
	}

	peerConnection.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		for _, client := range p.ClientMap {
			client.addRemoteTrack(track)
		}

		// 如果是视频 track, 则每3秒发送一次 PLI
		if track.Kind() == webrtc.RTPCodecTypeVideo {

			go func() {
				ticker := time.NewTicker(time.Second * 5)
				for range ticker.C {
					errSend := peerConnection.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: uint32(track.SSRC())}})
					if errSend != nil {
						fmt.Println(errSend)
					}
				}
			}()

			// 发送 PLI
			errSend := p.PeerConn.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: uint32(track.SSRC())}})
			if errSend != nil {
				fmt.Println(errSend)
			}
		}

		byteBuffer := make([]byte, 1500*1000)
		for {
			// 从 track 中读取数据
			i, _, err := track.Read(byteBuffer)
			if err != nil {
				break
			}
			// 将数据写到每一个 client 的 trackChan 中
			for _, client := range p.ClientMap {
				trackInfo, flag := client.trackChan[track.ID()]
				if !flag {
					continue
				}
				if trackInfo != nil {
					trackInfo.trackChan <- byteBuffer[:i]
				}
			}
		}

		// 从所有客户端中删除对应id的track并关闭对应的chan
		for _, client := range p.ClientMap {
			client.removeRemoteTrack(track.ID())
		}
	})

	PullServerCache[channelId.String()] = p

	p.Create()
	return p
}

func DeletePullServer(channelId string) {
	p := PullServerCache[channelId]
	if p == nil {
		return
	}
	for _, client := range p.ClientMap {
		client.Close()
	}
	p.Close()
	delete(PullServerCache, channelId)
}

func (p *PullServer) addRemoteTrack(track *webrtc.TrackRemote) *webrtc.TrackLocalStaticRTP {
	// 如果已经有该 track 了, 则直接返回
	if p.trackChan[track.ID()] != nil {
		return nil
	}

	newTrack, newTrackErr := webrtc.NewTrackLocalStaticRTP(track.Codec().RTPCodecCapability, track.ID(), track.StreamID())
	if newTrackErr != nil {
		panic(newTrackErr)
	}
	rtpSender, err := p.PeerConn.AddTrack(newTrack)
	if err != nil {
		panic(err)
	}
	// 创建接收 track 数据的通道
	trackChan := make(chan []byte)

	trackInfo := &trackInfo{
		trackChan: trackChan,
		rtpSender: rtpSender,
	}

	p.trackChan[track.ID()] = trackInfo
	// 创建转发协程
	go func() {
		for {
			data, err := <-trackChan
			if !err {
				break
			}
			newTrack.Write(data)
		}
	}()

	// 尝试对客户端创建 offer
	CreateOffer(p.Id, p.PeerConn, p.SignalConn, "server")

	return newTrack
}

func (p *PullServer) removeRemoteTrack(trackId string) {
	trackInfo, err := p.trackChan[trackId]
	if !err {
		return
	}
	delete(p.trackChan, trackId)
	close(trackInfo.trackChan)
	p.PeerConn.RemoveTrack(trackInfo.rtpSender)
}

func (p *PullServer) Join(push *PullServer) {
	// 如果已经有该客户端了, 则直接返回
	if p.ClientMap[push.Id] != nil {
		return
	}

	p.ClientMap[push.Id] = push
	// 获得当前 p.PeerConn 中的所有 track 并 add 到 push 中
	for _, track := range p.PeerConn.GetTransceivers() {
		receiver := track.Receiver()
		if receiver != nil {
			tracks := receiver.Tracks()
			// 遍历 tracks
			for _, track := range tracks {
				push.addRemoteTrack(track)
				println("add track id=" + track.ID() + " to " + push.Id + " success")
				if track.Kind() == webrtc.RTPCodecTypeVideo {
					// 发送 PLI
					errSend := p.PeerConn.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: uint32(track.SSRC())}})
					if errSend != nil {
						fmt.Println(errSend)
					}
				}
			}
		}
	}
}

func (p *PullServer) Leave(id string) {
	cli := p.ClientMap[id]
	if cli == nil {
		return
	}
	for trackId := range cli.trackChan {
		cli.removeRemoteTrack(trackId)
	}
	delete(p.ClientMap, id)
}

func (p *PullServer) Create() {
	// 发送加入房间消息
	createMessage := BindMessage{
		ChannelId: p.Id,
		Role:      "server",
		Type:      "create",
	}
	err := p.SignalConn.WriteJSON(createMessage)
	if err != nil {
		log.Println("write:", err)
	}
}

func (p *PullServer) Close() {
	p.SignalConn.Close()
	p.PeerConn.Close()
	p.CloseChan <- struct{}{}
	// 所有包含该频道的客户端的转发通道都关闭
}

func (p *PullServer) detectNetworkStat() {
	// 创建一个定时器，每隔10秒获取一次统计信息
	detectTimer := time.NewTicker(10 * time.Second)
	for range detectTimer.C {
		// 获取RTCP统计信息
		stats := p.PeerConn.GetStats()
		fmt.Println("Network Conditions:")
		fmt.Printf("Available Bandwidth: %d bps\n", stats["availableBandwidth"])
		fmt.Printf("RTT (Round-Trip Time): %d ms\n", stats["rtt"])
		fmt.Printf("Loss Rate: %f\n", stats["lossRate"])
		fmt.Println("Device Performance:")
		fmt.Printf("CPU Usage: %f%%\n", stats["cpuUsage"])
		fmt.Printf("Memory Usage: %d MB\n", stats["memoryUsage"])

		// 根据 cpu , memory , lossRate , rtt 等信息判断网络状况, 决定视频发送的码率

	}
}
