# 内存增长诊断

在同一个进程中采两次：刚启动并加载三个会话、正常聊天后全部任务停止。保持同样窗口尺寸、同一个 Tab 和滚动位置，每次在聊天页按 **Ctrl+Shift+F12**，或选择“设置 → 关于 → 导出内存诊断”。导出后会打开数据目录下的 `diagnostics/memory-*` 文件夹。不要用重启后的进程作为增长后的样本。

采样会主动 GC 和归还 Go 空闲页，可能短暂停顿；只在手动诊断时执行。默认不启动监听端口，不改变 GC 策略，不定时清理。导出数值统计和调用栈，不主动序列化聊天正文或配置。

`memory.json` 包含进程 PID、UI 数量及三次采样：

- `before_gc`：回收前。
- `after_gc`：垃圾回收后，观察仍存活的 `go.HeapAlloc`、`HeapObjects`。
- `after_release`：再调用 `debug.FreeOSMemory`，观察 `HeapReleased` 和 Windows 的 `privateBytes`、`workingSetBytes`。

判断时比较两份报告：

- GC 后的 HeapAlloc 持续增长：查 `heap.pprof` 的存活对象分配栈。
- 回收前大、回收后明显下降：已不可达的临时分配；查 `allocs.pprof` 的累计分配栈。
- HeapAlloc 小，但释放空闲页后 privateBytes 明显下降：Go 保留的空闲页占比较大。
- Go 的 `Sys - HeapReleased` 比较稳定、privateBytes 却增长：继续查 Go 堆之外的原生 UI、字体、位图、线程栈等。两者之差不是原生内存的精确计量；Go pprof 也不包含原生分配调用栈。
- 工作集下降不等于对象已释放；必须一起看私有内存和 Go 堆。

在仓库根目录执行（替换为两次导出的实际路径）：

```bash
go tool pprof -top -sample_index=inuse_space after/heap.pprof
go tool pprof -top -sample_index=inuse_space -base before/heap.pprof after/heap.pprof
go tool pprof -top -sample_index=alloc_space -base before/allocs.pprof after/allocs.pprof
go tool pprof -top after/goroutine.pprof
```

二进制 profile 自带 Go 符号。堆采样为统计采样，对小对象和短时间差可能有误差。需要追到源码时保留对应版本的源码和构建产物。`uiCounts` 用于对照会话、Markdown、附件、文件和预览数量，计数不代表其字节占用。

## 2026-10-30 样本与修复

用户提供的使用后样本 `memory-20261030-111455-2042390680` 与刚启动样本 `memory-20261030-111523-1062241949` 来自不同 PID；前者3个 Tab、6个会话对象、439条消息且有1个任务运行，后者只有1个空会话，不能据此证明持续泄漏。

使用后私有内存237.44MiB，归还空闲页后191.84MiB，存活堆42.95MiB；启动样本分别152.28、139.23、15.16MiB。`toolResult` 累计分配调用链约1.9GiB，占累计分配约46%，不是同时存活内存。

修复采用一个数据表示：事件及历史加载时把结果 JSON 替换为解码后的 map，去掉相同的 body 原文；展示时就地读取协议 data 和错误元数据，不保留额外的原文/缓存副本。新结果替换旧结果，兼容历史字符串与快照往返。最近历史会话仅缓存一份可丢弃正文，草稿、待发附件、附件加载和运行中的状态继续保留；Mermaid 脚本首次预览才加载。

64KB 工具结果的 `BenchmarkToolResultRead` 在本机：旧路径约56µs、132102 B、16次分配；结构化路径约85ns、0 B、0次分配。这只说明重复读取的分配已消除，不代表整体进程必然减少相同字节。运行后需再次采样验证真实占用。

快照中 `previewEntries=0`，但堆采样仍出现预览文档分配栈，尚未确定其引用来源；本次没有推测性修改 WebView 销毁或 MyGo 源码。


后续新版样本 `memory-20261030-114045-1032709496` 已确认结果解析路径降至累计分配的0.3%以下，但预览计数为0时，`previewReadyDocument` 仍保留约31.5MiB。Windows MyGo `LoadHTML` 将正文存入底层 `htmlFor`，销毁路径未清该表；仅凭堆采样不能确认底层窗口的完整引用链。应用现改为 `ally-preview://document/<随机ID>`：独立注册的协议处理器按URL读取正文，nativePreview换页/销毁立即撤销旧URL，响应no-store；不再让LoadHTML缓存大文档。回归测试核验撤销后返回404，并需继续用新版堆快照验证原生端实际释放。
