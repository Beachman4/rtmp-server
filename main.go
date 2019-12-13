package main

import (
	"errors"
	"flag"
	"fmt"
	"github.com/julienschmidt/httprouter"
	"github.com/nareix/joy4/av"
	"github.com/nareix/joy4/av/avutil"
	"github.com/nareix/joy4/av/pktque"
	"github.com/nareix/joy4/av/pubsub"
	"github.com/nareix/joy4/format"
	"github.com/nareix/joy4/format/rtmp"
	"net/http"
	"sync"
	"time"
)

var (
	sKey = flag.String("k", "test", "Stream key, to protect your stream")
)


type FrameDropper struct {
	Interval     int
	n            int
	skipping     bool
	DelaySkip    time.Duration
	lasttime     time.Time
	lastpkttime  time.Duration
	delay        time.Duration
	SkipInterval int
}

func (self *FrameDropper) ModifyPacket(pkt *av.Packet, streams []av.CodecData, videoidx int, audioidx int) (drop bool, err error) {
	if self.DelaySkip != 0 && pkt.Idx == int8(videoidx) {
		now := time.Now()
		if !self.lasttime.IsZero() {
			realdiff := now.Sub(self.lasttime)
			pktdiff := pkt.Time - self.lastpkttime
			self.delay += realdiff - pktdiff
		}
		self.lasttime = time.Now()
		self.lastpkttime = pkt.Time

		if !self.skipping {
			if self.delay > self.DelaySkip {
				self.skipping = true
				self.delay = 0
			}
		} else {
			if pkt.IsKeyFrame {
				self.skipping = false
			}
		}
		if self.skipping {
			drop = true
		}

		if self.SkipInterval != 0 && pkt.IsKeyFrame {
			if self.n == self.SkipInterval {
				self.n = 0
				self.skipping = true
			}
			self.n++
		}
	}

	if self.Interval != 0 {
		if self.n >= self.Interval && pkt.Idx == int8(videoidx) && !pkt.IsKeyFrame {
			drop = true
			self.n = 0
		}
		self.n++
	}

	return
}

func copyPackets(src av.PacketReader, rtmps []av.Muxer) (err error) {
	var pkgChans []chan av.Packet
	for _, conn := range rtmps {
		pktChan := make(chan av.Packet)
		pkgChans = append(pkgChans, pktChan)

		go func(conn av.Muxer, pkgChan <-chan av.Packet) {
			for pkt := range pkgChan {
				if err = conn.WritePacket(pkt); err != nil {
					return
				}
			}
		}(conn, pktChan)
	}

	sourceChan := make(chan av.Packet, 1)
	errorChan := make(chan error, 1)
	go func() {
		for {
			var pkt av.Packet
			if pkt, err = src.ReadPacket(); err != nil {
				errorChan <- err
				break
			} else {
				sourceChan <- pkt
			}
		}
	}()

	for {
		select {
		case pkt := <-sourceChan:
			fmt.Println(fmt.Sprintf("size: %v", len(pkt.Data)))
			fmt.Println(fmt.Sprintf("Time: %f", pkt.Time.Seconds()))
			for _, pkgChan := range pkgChans {
				pkgChan <- pkt
			}
		case err = <-errorChan:
			return
		case <-time.After(time.Second * 8):
			err = errors.New("Packet timeout reached")
			return
		}
	}
}

func writeHeaders(src av.Demuxer, rtmps []av.Muxer) (err error) {
	var streams []av.CodecData
	if streams, err = src.Streams(); err != nil {
		return
	}

	for _, conn := range rtmps {
		if err = conn.WriteHeader(streams); err != nil {
			return
		}
	}

	return
}

func closeConnections(rtmps []*rtmp.Conn) (err error) {
	for _, conn := range rtmps {
		if err = conn.WriteTrailer(); err != nil {
			return
		}
		conn.Close()
	}
	return
}

func init() {
	format.RegisterAll()
}


func main() {
	flag.Parse()
	server := &rtmp.Server{}

	rwMutex := &sync.RWMutex{}
	type Channel struct {
		que *pubsub.Queue
	}
	channels := map[string]*Channel{}

	server.HandlePlay = func(conn *rtmp.Conn) {
		rwMutex.RLock()
		ch := channels[conn.URL.Path]
		rwMutex.RUnlock()

		if ch != nil {
			cursor := ch.que.Latest()
			query := conn.URL.Query()

			if q := query.Get("delaygop"); q != "" {
				n := 0
				fmt.Sscanf(q, "%d", &n)
				cursor = ch.que.DelayedGopCount(n)
			} else if q := query.Get("delaytime"); q != "" {
				dur, _ := time.ParseDuration(q)
				cursor = ch.que.DelayedTime(dur)
			}

			filters := pktque.Filters{}

			if q := query.Get("waitkey"); q != "" {
				filters = append(filters, &pktque.WaitKeyFrame{})
			}

			filters = append(filters, &pktque.FixTime{StartFromZero: true, MakeIncrement: true})

			if q := query.Get("framedrop"); q != "" {
				n := 0
				fmt.Sscanf(q, "%d", &n)
				filters = append(filters, &FrameDropper{Interval: n})
			}

			if q := query.Get("delayskip"); q != "" {
				dur, _ := time.ParseDuration(q)
				skipper := &FrameDropper{DelaySkip: dur}
				if q := query.Get("skipinterval"); q != "" {
					n := 0
					fmt.Sscanf(q, "%d", &n)
					skipper.SkipInterval = n
				}
				filters = append(filters, skipper)
			}

			demuxer := &pktque.FilterDemuxer{
				Filter:  filters,
				Demuxer: cursor,
			}

			avutil.CopyFile(conn, demuxer)
		}
	}

	server.HandlePublish = func(conn *rtmp.Conn) {
		//streams, _ := conn.Streams()

		rwMutex.Lock()
		fmt.Println("request string->", conn.URL.RequestURI())
		fmt.Println("request key->", conn.URL.Query().Get("key"))
		//streamKey := conn.URL.Query().Get("key")

		key := conn.URL.RequestURI()[1:]

		fmt.Println(key)

		ch := channels[conn.URL.Path]
		if ch == nil {
			ch = &Channel{}
			ch.que = pubsub.NewQueue()
			channels[conn.URL.Path] = ch
		} else {
			fmt.Println("Channel is not nil")
			ch = nil
		}
		rwMutex.Unlock()
		if ch == nil {
			fmt.Println("Channel is nil")
			return
		}


		go func() {
			time.Sleep(time.Second * 3)

			//resty.R().Get(fmt.Sprintf("http://35.238.243.208:8080/start-transcoding/%s", key))
		}()

		go func() {

		}()
		//go func() {
		//	for {
		//		c := conn.NetConn()
		//		one := make([]byte, 1)
		//		c.SetReadDeadline(time.Now())
		//		if _, err := c.Read(one); err == io.EOF {
		//			conn.Close()
		//			c = nil
		//			break;
		//		} else {
		//			c.SetReadDeadline(time.Now().Add(10 * time.Millisecond))
		//		}
		//
		//		time.Sleep(time.Second * 3)
		//	}
		//}()

		fmt.Println("Starting copy")

		//restream, _ := rtmp.Dial("rtmp://a.rtmp.youtube.com/live2/5zbw-f7kv-y239-fdke")

		rtmpConns := []av.Muxer{
			//restream,
			ch.que,
		}

		err := writeHeaders(conn, rtmpConns)

		if err != nil {
			fmt.Println(err)
		}

		err = copyPackets(conn, rtmpConns)

		if err != nil {
			fmt.Println(err)
		}

		err = closeConnections([]*rtmp.Conn{
			//restream,
		})

		if err != nil {
			fmt.Println(err)
		}

		err = ch.que.WriteTrailer()

		if err != nil {
			fmt.Println(err)
		}

		//err := avutil.CopyFile(ch.que, conn)

		fmt.Println("Stream is done....")

		rwMutex.Lock()
		delete(channels, conn.URL.Path)
		rwMutex.Unlock()
		ch.que.Close()

		//resty.R().Get(fmt.Sprintf("http://35.238.243.208:8080/stop-transcoding/%s", key))

		fmt.Println("Cleanup done")
	}

	router := httprouter.New()
	router.GET("/healthz", func(writer http.ResponseWriter, request *http.Request, params httprouter.Params) {
		fmt.Fprint(writer, "Healthy!\n")
	})

	go http.ListenAndServe(":8080", router)

	server.ListenAndServe()
}

func pushStream(sub *pubsub.Queue, dsturl string, wait *sync.WaitGroup) error {

	defer wait.Done()

	origin := sub.Latest()
	filters := pktque.Filters{}

	filters = append(filters, &pktque.FixTime{StartFromZero: true, MakeIncrement: true})
	demuxer := &pktque.FilterDemuxer{
		Filter:  filters,
		Demuxer: origin,
	}

	fmt.Println(dsturl)

	dst, err := rtmp.Dial(dsturl)
	if err != nil {
		fmt.Println("error")
		return err
	}
	defer dst.Close()

	return avutil.CopyFile(dst, demuxer)
}