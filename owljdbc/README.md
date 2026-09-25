# owljdbc — JDBC 通道驱动（owl agent driver）

把「只有 JDBC 驱动」的数据库伪装成 Go `database/sql` 驱动：注册名 `owljdbc`，
底层通过子进程 JVM sidecar（`jvm/owl-agent`，≈700 行自研 java.sql 封装）执行
JDBC，帧协议见 `docs` 或父仓库计划文档。

## 接入（两行）

```go
import _ "github.com/cangyunye/owljdbc" // init() 注册 "owljdbc" 驱动

db, err := sql.Open("owljdbc", owljdbc.EncodeDSN(owljdbc.Config{
    DriverClass: "com.oceanbase.jdbc.Driver",
    URL:         "jdbc:oceanbase://127.0.0.1:2881?useSSL=false&characterEncoding=UTF-8",
    User:        "root@obmysql",
    Password:    "***",
    Family:      "mysql",            // mysql | oracle | postgres
    Classpath:   []string{"oceanbase-client-2.4.1.jar"},
    AgentJar:    "owl-agent.jar",    // bash jvm/owl-agent/build.sh 构建
}))
```

只 import 不调用也行：包 `init()` 完成注册，通道在首次 `sql.Open` 后惰性 spawn
JVM sidecar（1 个 classpath profile = 1 个 JVM，多连接复用，全部关闭即回收）。

## Catalog（按库接入目录）

解析好连接要素（host/port/user/password/db）后可用内置目录组装配置，免手写
driverClass/URL：

```go
cfg, err := owljdbc.BuildConfig("oceanbase-mysql",
    owljdbc.Endpoint{Host: "127.0.0.1", Port: "2881", User: "root@obmysql",
        Password: "***", Database: "app"},
    "" /* dsn（TimesTen 等原样透传场景才需要） */, "./jars", "", "")
```

已登记：oceanbase-mysql / oceanbase-oracle（实测）、mysql / postgres（实测）、
oracle / goldendb-mysql / dm / kingbase / timesten（登记未测）。接入新库 =
驱动 jar + family + URL 模板，无需改通道代码。

## 构建 sidecar

```bash
bash jvm/owl-agent/build.sh   # 产出 jvm/owl-agent/owl-agent.jar（JRE 8+ 可跑）
```

驱动 jar 下载脚本见 `scripts/fetch-jars.sh`（Maven Central，版本见脚本内常量）。

## 测试

```bash
go test ./...                      # 单测（fake sidecar，无需 JVM/真库）
go test -tags e2e ./...            # 集成（需真库与 jar）
```
