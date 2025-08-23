package core

import (
	"PlayFast/internal/api"
	"PlayFast/internal/echo"
	"PlayFast/internal/http-client"
	"PlayFast/internal/node"
	"PlayFast/internal/path"
	"PlayFast/utils"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"time"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/experimental/deprecated"
	"github.com/sagernet/sing-box/include"
	slog "github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	dns "github.com/sagernet/sing-dns"
	"github.com/sagernet/sing/common/json/badoption"
	"github.com/sagernet/sing/service"
)

//go:embed geoip-cn.json
var geoipJson []byte

//go:embed geosite-cn.srs
var geosite []byte

//go:embed black-list.json
var black []byte

//go:embed direct-list.json
var direct []byte

type Box struct {
	box              *box.Box
	ctx              context.Context
	router           bool
	appends          []string
	defaultInterface int
	sync.Mutex
}

func (b *Box) Start(region string, router bool) error {
	b.Lock()
	defer b.Unlock()
	b.router = router
	if b.box == nil {
		err := b.newBox(region)
		if err != nil {
			return err
		}
	}
	err := b.box.Start()
	if err != nil {
		return err
	}
	if router {
		var defaultInterface *net.Interface
		defaultInterface, err = utils.GetDefaultInterface()
		if err != nil {
			return err
		}
		b.defaultInterface = defaultInterface.Index
		err = utils.SetIPForwarding(b.defaultInterface, true)
	}
	return route(b.appends)
}
func (b *Box) Stop() error {
	b.Lock()
	defer b.Unlock()
	if b.box == nil {
		return nil
	}
	err := b.box.Close()
	b.box = nil
	if b.router {
		err = utils.SetIPForwarding(b.defaultInterface, false)
		if err != nil {
			return err
		}
	}
	deleteRoute(b.appends)
	return err
}
func New(ctx context.Context) *Box {
	ctx = service.ContextWith(ctx, deprecated.NewStderrManager(slog.StdLogger()))
	ctx = box.Context(ctx, include.InboundRegistry(), include.OutboundRegistry(), include.EndpointRegistry(), include.DNSTransportRegistry())
	var data []byte
	data, err := http_client.GET(fmt.Sprintf("%s/black-list.json", api.GetApiDomain()))
	if err != nil {
		data = black
	}
	_ = os.WriteFile(filepath.Join(path.Path(), "black-list.json"), data, 0644)

	data, err = http_client.GET(fmt.Sprintf("%s/direct-list.json", api.GetApiDomain()))
	if err != nil {
		data = direct
	}
	_ = os.WriteFile(filepath.Join(path.Path(), "direct-list.json"), data, 0644)

	data, err = http_client.GET("https://raw.githubusercontent.com/lyc8503/sing-box-rules/refs/heads/rule-set-geoip/geoip-cn.json")
	if err != nil {
		data = geoipJson
	}
	_ = os.WriteFile(filepath.Join(path.Path(), "geoip-cn.json"), data, 0644)

	data, err = http_client.GET("https://raw.githubusercontent.com/lyc8503/sing-box-rules/refs/heads/rule-set-geosite/geosite-cn.srs")
	if err != nil {
		data = geosite
	}
	_ = os.WriteFile(filepath.Join(path.Path(), "geosite-cn.srs"), data, 0644)
	return &Box{
		ctx: ctx,
	}
}

func (b *Box) newBox(proxy string) error {
	data := node.Get()
	var localResolutionDomainName []string
	var proxyOutbound *option.Outbound
	var proxyOutboundIP string
	b.appends = make([]string, 0)
	for i, p := range data {
		switch {
		case p.Name == proxy:
			localResolutionDomainName = append(localResolutionDomainName, p.Host)
		default:
			continue
		}
		var out option.Outbound
		switch p.Protocol {
		case "shadowsocks":
			out = option.Outbound{
				Type: constant.TypeShadowsocks,
				Tag:  "proxy",
				Options: &option.ShadowsocksOutboundOptions{
					ServerOptions: option.ServerOptions{
						Server:     p.Host,
						ServerPort: p.Port,
					},
					Method:   p.Method,
					Password: p.Password,
					UDPOverTCP: &option.UDPOverTCPOptions{
						Enabled: true,
						Version: 2,
					},
				},
			}
		case "vless":
			out = option.Outbound{
				Type: constant.TypeVLESS,
				Tag:  "proxy",
				Options: &option.VLESSOutboundOptions{
					ServerOptions: option.ServerOptions{
						Server:     p.Host,
						ServerPort: p.Port,
					},
					UUID:                        p.Password,
					OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{},
					Multiplex: &option.OutboundMultiplexOptions{
						Enabled:        true,
						Protocol:       "h2mux",
						MaxConnections: 8,
						MinStreams:     16,
						Padding:        false,
					},
				},
			}
		case "socks":
			out = option.Outbound{
				Type: constant.TypeSOCKS,
				Tag:  "proxy",
				Options: &option.SOCKSOutboundOptions{
					ServerOptions: option.ServerOptions{
						Server:     p.Host,
						ServerPort: p.Port,
					},
					Version:  "5",
					Username: "playfast",
					Password: p.Password,
					UDPOverTCP: &option.UDPOverTCPOptions{
						Enabled: true,
						Version: 2,
					},
				},
			}
		default:
			continue
		}
		if p.Name == proxy {
			proxyOutbound = &out
			proxyOutboundIP = p.Host
			continue
		}
		registryOut := include.OutboundRegistry()
		createOutbound, err2 := registryOut.CreateOutbound(context.Background(), nil, slog.StdLogger(), out.Type, out.Type, out.Options)
		if err2 != nil {
			continue
		}
		client := echo.NewClient("1.1.1.1:80", echo.WithTimeout(3*time.Second), echo.WithDialer(createOutbound.DialContext))
		err := client.Connect(b.ctx)
		if err != nil {
			continue
		}
		var ms int64
		result := client.Test(b.ctx, []byte("GET / HTTP/1.1\r\nHost: 1.1.1.1\r\nAccept: *\r\n\r\n\r\n"))
		if result.Success {
			ms = result.Latency.Milliseconds()
		}
		log.Println(fmt.Sprintf("节点选择:ID:%d 节点:%s 延迟=%dms\n", i, p.Name, ms))
		if ms <= 0 {
			return errors.New("节点超时")
		}
	}
	if proxyOutbound == nil {
		return errors.New("not fount Outbound")
	}
	b.appends = append(b.appends, fmt.Sprintf("%s/32", proxyOutboundIP))
	options := box.Options{
		Options: option.Options{
			Log: &option.LogOptions{
				Disabled: true,
			},
			DNS: &option.DNSOptions{
				RawDNSOptions: option.RawDNSOptions{
					Servers: []option.DNSServerOptions{
						{
							Type: "https",
							Tag:  "proxyDns",
							Options: &option.RemoteHTTPSDNSServerOptions{
								RemoteTLSDNSServerOptions: option.RemoteTLSDNSServerOptions{
									RemoteDNSServerOptions: option.RemoteDNSServerOptions{
										LocalDNSServerOptions: option.LocalDNSServerOptions{
											DialerOptions: option.DialerOptions{
												Detour: "proxy",
											},
										},
										DNSServerAddressOptions: option.DNSServerAddressOptions{
											Server:     "cloudflare-dns.com",
											ServerPort: 443,
										},
									},
								},
							},
						},
						{
							Type: "https",
							Tag:  "localDns",
							Options: &option.RemoteHTTPSDNSServerOptions{
								RemoteTLSDNSServerOptions: option.RemoteTLSDNSServerOptions{
									RemoteDNSServerOptions: option.RemoteDNSServerOptions{
										DNSServerAddressOptions: option.DNSServerAddressOptions{
											Server:     "223.5.5.5",
											ServerPort: 443,
										},
									},
								},
							},
						},
					},
					Rules: []option.DNSRule{
						{
							Type: constant.RuleTypeDefault,
							DefaultOptions: option.DefaultDNSRule{
								RawDefaultDNSRule: option.RawDefaultDNSRule{
									RuleSet: []string{
										"geosite-cn",
									},
								},
								DNSRuleAction: option.DNSRuleAction{
									Action: constant.RuleActionTypeRoute,
									RouteOptions: option.DNSRouteActionOptions{
										Server: "localDns",
									},
								},
							},
						},
						{
							Type: constant.RuleTypeDefault,
							DefaultOptions: option.DefaultDNSRule{
								RawDefaultDNSRule: option.RawDefaultDNSRule{
									Domain: localResolutionDomainName,
								},
								DNSRuleAction: option.DNSRuleAction{
									Action: constant.RuleActionTypeRoute,
									RouteOptions: option.DNSRouteActionOptions{
										Server: "localDns",
									},
								},
							},
						},
					},
					Final: "proxyDns",
					DNSClientOptions: option.DNSClientOptions{
						Strategy:      option.DomainStrategy(dns.DomainStrategyUseIPv4),
						CacheCapacity: 2048,
					},
				},
			},
			Inbounds: []option.Inbound{
				{
					Type: constant.TypeTun,
					Tag:  "tun-in",
					Options: &option.TunInboundOptions{
						InterfaceName: "utun25",
						MTU:           1500,
						Address: badoption.Listable[netip.Prefix]{
							netip.MustParsePrefix("172.25.0.0/30"),
						},
						//RouteAddress: in(),
						//AutoRoute:    true,
						//StrictRoute:  true,
						UDPTimeout: option.UDPTimeoutCompat(time.Second * 300),
						Stack:      "gvisor",
					},
				},
			},
			Route: &option.RouteOptions{
				RuleSet: []option.RuleSet{
					{
						Type:         constant.RuleSetTypeLocal,
						Tag:          "geosite-cn",
						Format:       constant.RuleSetFormatBinary,
						LocalOptions: option.LocalRuleSet{Path: filepath.Join(path.Path(), "geosite-cn.srs")},
					},
					{
						Type:         constant.RuleSetTypeLocal,
						Tag:          "geoip-cn",
						Format:       constant.RuleSetFormatBinary,
						LocalOptions: option.LocalRuleSet{Path: filepath.Join(path.Path(), "geoip-cn.srs")},
					},
					{
						Type:         constant.RuleSetTypeLocal,
						Tag:          "black-list",
						Format:       constant.RuleSetFormatSource,
						LocalOptions: option.LocalRuleSet{Path: filepath.Join(path.Path(), "black-list.json")},
					},
					{
						Type:         constant.RuleSetTypeLocal,
						Tag:          "direct-list",
						Format:       constant.RuleSetFormatSource,
						LocalOptions: option.LocalRuleSet{Path: filepath.Join(path.Path(), "direct-list.json")},
					},
				},
				AutoDetectInterface: true,
				Rules:               []option.Rule{},
			},
			Outbounds: []option.Outbound{
				*proxyOutbound, {Type: constant.TypeDirect, Tag: "direct"},
			},
			Experimental: &option.ExperimentalOptions{
				ClashAPI: &option.ClashAPIOptions{
					ExternalController: "127.0.0.1:54713",
				},
			},
		},
		Context: b.ctx,
	}
	options.Options.Route.Rules = append(options.Options.Route.Rules, []option.Rule{
		{
			Type: constant.RuleTypeDefault,
			DefaultOptions: option.DefaultRule{
				RawDefaultRule: option.RawDefaultRule{
					Network: []string{
						"udp",
					},
					Port: []uint16{
						443,
					},
				},
				RuleAction: option.RuleAction{
					Action: constant.RuleActionTypeReject,
					RejectOptions: option.RejectActionOptions{
						Method: "default",
						NoDrop: false,
					},
				},
			},
		}, //禁止http3
		{
			Type: constant.RuleTypeDefault,
			DefaultOptions: option.DefaultRule{
				RawDefaultRule: option.RawDefaultRule{
					Invert: true,
				},
				RuleAction: option.RuleAction{
					Action: constant.RuleActionTypeSniff,
					SniffOptions: option.RouteActionSniff{
						Sniffer: []string{
							"dns", "http", "tls", "quic",
						},
					},
				},
			},
		}, //解析协议域名
		{
			Type: constant.RuleTypeDefault,
			DefaultOptions: option.DefaultRule{
				RawDefaultRule: option.RawDefaultRule{
					RuleSet: []string{"black-list"},
				},
				RuleAction: option.RuleAction{
					Action: constant.RuleActionTypeReject,
					RejectOptions: option.RejectActionOptions{
						Method: constant.RuleActionRejectMethodDefault,
						NoDrop: false,
					},
				},
			},
		}, //过滤黑名单
		{
			Type: constant.RuleTypeDefault,
			DefaultOptions: option.DefaultRule{
				RawDefaultRule: option.RawDefaultRule{
					RuleSet: []string{"direct-list"},
				},
				RuleAction: option.RuleAction{
					Action: constant.RuleActionTypeRoute,
					RouteOptions: option.RouteActionOptions{
						Outbound: "direct",
					},
				},
			},
		}, //直连白名单
		{
			Type: constant.RuleTypeDefault,
			DefaultOptions: option.DefaultRule{
				RawDefaultRule: option.RawDefaultRule{
					Protocol: []string{"dns"},
				},
				RuleAction: option.RuleAction{
					Action: constant.RuleActionTypeHijackDNS,
				},
			},
		}, //dns劫持
		{
			Type: constant.RuleTypeDefault,
			DefaultOptions: option.DefaultRule{
				RawDefaultRule: option.RawDefaultRule{
					RuleSet: []string{
						"geosite-cn", "geoip-cn",
					},
				},
				RuleAction: option.RuleAction{
					Action: constant.RuleActionTypeRoute,
					RouteOptions: option.RouteActionOptions{
						Outbound: "direct",
					},
				},
			},
		}, //中国地区直连
		{
			Type: constant.RuleTypeDefault,
			DefaultOptions: option.DefaultRule{
				RawDefaultRule: option.RawDefaultRule{
					Invert: true,
				},
				RuleAction: option.RuleAction{
					Action: constant.RuleActionTypeRoute,
					RouteOptions: option.RouteActionOptions{
						Outbound: "proxy",
					},
				},
			},
		}, //最终代理
	}...)
	_ = os.Remove(path.Path() + "/run.log")
	options.Log = &option.LogOptions{
		Disabled:     false,
		Level:        slog.FormatLevel(slog.LevelInfo),
		Output:       path.Path() + "/run.log",
		Timestamp:    true,
		DisableColor: true,
	}
	var err error
	b.box, err = box.New(options)
	return err
}
