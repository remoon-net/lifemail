# lifemail

一个简单的内网 mail server, 适配了 Thunderbird 和 Delta Chat

# 支持的功能

- [x] 邮箱别名 (和 gmail 类似, 先分割`+`后缀, 后忽略`.`, )
- [x] 支持证书 TLS (默认不启用, 因为主要目的是用于[well-net](https://github.com/remoon-net/well.git)中)
- [x] 支持 Cloudflare Email Routing, 对应的 [worker.js](smtp/worker.js)

# Todo

- [x] 支持 Thunderbird
  - [x] 现在能显示文件夹邮件了
  - [x] 支持推送
  - [x] Drafts 保存有问题 (在 Select 的时候就开始订阅数据库变更就好)
  - [x] MailUpdate 好像没有缓冲, 一有就 Poll 了, 看起来和 impmemserver 的实现不一样, 也许需要优化(优化上一条的时候修复了)
- [ ] 支持 Active Exchange Sync, 以便在手机上直接添加日程 (life 之意)

# 参考项目

- [maddy](https://github.com/foxcpp/maddy.git)
