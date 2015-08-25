package teleport

import (
	"log"
	"net"
	"time"
)

// 客户端专有成员
type tpClient struct {
	// 客户端模式下，控制是否为短链接
	short bool
	// 强制终止客户端
	mustClose bool
}

// 启动客户端模式
func (self *TP) Client(serverAddr string, port string, isShort ...bool) {
	if len(isShort) > 0 && isShort[0] {
		self.tpClient.short = true
	} else if self.timeout == 0 {
		// 默认心跳频率为3秒1次
		self.timeout = 3e9
	}
	self.reserveAPI()
	self.mode = CLIENT
	self.port = port
	self.serverAddr = serverAddr

	self.tpClient.mustClose = false

	go self.apiHandle()
	go self.client()
}

// 设置客户端唯一标识符，默认为本节点ip:port，对服务器模式无效，服务器模式的UID强制为“Server”
func (self *TP) SetUID(nodeuid string) Teleport {
	if self.mode == SERVER {
		return self
	}
	self.uid = nodeuid
	return self
}

// ***********************************************功能实现*************************************************** \\

// 以客户端模式启动
func (self *TP) client() {
	log.Println(" *     —— 正在连接服务器……")

RetryLabel:
	conn, err := net.Dial("tcp", self.serverAddr+self.port)
	if err != nil {
		if self.tpClient.mustClose {
			self.tpClient.mustClose = false
			return
		}
		time.Sleep(1e9)
		goto RetryLabel
	}
	// log.Printf(" *     —— 成功连接到服务器：%v ——", conn.RemoteAddr().String())

	// 开启该连接处理协程(读写两条协程)
	self.cGoConn(conn)

	// 与服务器意外断开后自动重拨
	if !self.short {
		for self.CountNodes() > 0 {
			time.Sleep(1e9)
		}
		// 判断是否为意外断开
		if _, ok := self.connPool["Server"]; ok {
			goto RetryLabel
		}
	}
}

// 为每个连接开启读写两个协程
func (self *TP) cGoConn(conn net.Conn) {
	remoteAddr, connect := NewConnect(conn, self.connBufferLen, self.connWChanCap)
	self.connPool["Server"] = connect
	// 绑定节点UID与conn
	if self.uid == "" {
		self.uid = conn.LocalAddr().String()
	}

	if !self.short {
		self.send(NewNetData(self.uid, "Server", IDENTITY, ""))
	}

	// 标记连接已经正式生效可用
	self.connPool["Server"].UID = remoteAddr

	log.Printf(" *     —— 成功连接到服务器：%v (%v)——", "Server", remoteAddr)
	// 开启读写双工协程
	go self.cReader("Server")
	go self.cWriter("Server")
}

// 客户端读数据
func (self *TP) cReader(nodeuid string) {
	// 退出时关闭连接，删除连接池中的连接
	defer func() {
		self.closeConn(nodeuid, true)
	}()

	var conn = self.getConn(nodeuid)

	for {
		if !self.read(conn) {
			break
		}
	}
}

// 客户端发送数据
func (self *TP) cWriter(nodeuid string) {
	// 退出时关闭连接，删除连接池中的连接
	defer func() {
		self.closeConn(nodeuid, true)
	}()

	var conn = self.getConn(nodeuid)

	for conn != nil {
		if self.short {
			self.send(<-conn.WriteChan)
			continue
		}

		timing := time.After(self.timeout)
		data := new(NetData)
		select {
		case data = <-conn.WriteChan:
		case <-timing:
			// 保持心跳
			data = NewNetData(self.uid, nodeuid, HEARTBEAT, "")
		}

		self.send(data)
	}
}
