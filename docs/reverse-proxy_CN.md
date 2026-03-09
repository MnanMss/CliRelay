# NGINX 反向代理配置指北

如果你在服务器上部署了 `CliRelay` 后端，并且使用了配套的前端管理面板 [codeProxy](https://github.com/kittors/codeProxy)，你可能希望使用一个美观的域名（例如 `https://cliproxy.yourdomain.com/manage/`）来统一访问它们，而不是分别通过 IP 和不同的端口号（例如 `8317` 和 `80`）。

这篇指南将告诉你如何通过 NGINX 配置，将**前端面板的静态页面**与**后端的动态 API** 合并在同一个域名下提供服务。

## 前置条件

1. 你已经有一个备案 / 可用的域名解析到了你的服务器 IP。
2. 你的服务器上安装了 NGINX。
3. 你已经通过 Docker 或直接运行的方式启动了后端 API（默认端口 `8317`）。
4. 你已经启动了 `codeProxy` 前端 WebApp 服务（假设运行在 `3005` 或 Docker 映射的 `80` 等其他端口，本例假设前端面板跑在本地的 `8080` 端口）。

## 核心配置思路

我们要实现的效果是：
- 用户访问 `https://cliproxy.yourdomain.com/` 或者 `https://cliproxy.yourdomain.com/manage/*` 时，NGINX 会将请求交给前端页面服务。
- 只有当请求进入类似 `https://cliproxy.yourdomain.com/v1/*` 或 `https://cliproxy.yourdomain.com/api/*` 时，NGINX 才会识别出这是 API 调用，从而反向代理给后端的 `8317` 端口。

## 典型 NGINX 配置模板

你可以将以下内容保存到你的 NGINX `conf.d` 目录下，例如 `/etc/nginx/conf.d/cliproxy.conf`。请根据你实际的环境修改域名和端口号。

```nginx
server {
    listen 80;
    server_name cliproxy.yourdomain.com; # 替换成你的域名

    # 1. 代理前端面板页面请求
    # 将所有的根路径请求都交给前端面板服务
    location / {
        proxy_pass http://127.0.0.1:8080; # 请替换为你的前端 Web 容器/服务实际运行的端口
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        
        # 支持 WebSocket (React/Vite HMR 或实时告警需要)
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
    }

    # 2. 代理后端 OpenAI 兼容标准接口
    # 比如 /v1/chat/completions 或者 /v1/models
    location /v1/ {
        proxy_pass http://127.0.0.1:8317; # 后端 CliRelay 的默认端口
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        # 必须要加：开启 SSE（Server-Sent Events）流式输出支持
        proxy_buffering off;
        proxy_cache off;
    }

    # 3. 提供前端面板界面的基础路由
    # 对于用户直接访问 /manage，最好重定向到 /manage/dashboard，避免 404
    location = /manage {
        return 301 /manage/dashboard;
    }

    # 4. 代理后端管理专属私有 API
    # 比如拉取系统运行状态、操作 Token、读取监控图表等 /manage/ 或 /api/ 下的请求
    location /manage/ {
        proxy_pass http://127.0.0.1:8317; # 指向 CliRelay 后端
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

## 配置生效

保存好配置文件后，检查配置语法是否正确：
```bash
nginx -t
```
如果提示 `syntax is ok`，即可重载 NGINX 使其生效：
```bash
systemctl reload nginx
```

现在，你就可以在浏览器里直接访问 `http://cliproxy.yourdomain.com/` 打开美观的监控面板，而在你的终端/光标/客户端代码里，只需配置 `http://cliproxy.yourdomain.com/v1/` 就能自动穿透调用后端的模型代理了！
