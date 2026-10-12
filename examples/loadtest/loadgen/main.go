// Command loadgen writes realistic application log lines for load-testing the
// grepalert agent: fixed or unlimited rate, a configurable share of ERROR
// lines, optional size limit and logrotate-style rotation.
//
//	loadgen -out app.log -size 5GB                 # write 5 GB as fast as possible
//	loadgen -out app.log -rate 20MB -rotate 1GB    # 20 MB/s forever, rotate at 1 GB
package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"strconv"
	"strings"
	"time"
)

func main() {
	out := flag.String("out", "app.log", "file to append to")
	rateS := flag.String("rate", "0", "bytes per second, e.g. 20MB (0 = as fast as possible)")
	sizeS := flag.String("size", "0", "stop after writing this much, e.g. 5GB (0 = never)")
	rotateS := flag.String("rotate", "0", "rename the file to <out>.1 when it reaches this size (0 = never)")
	errPct := flag.Float64("errors", 1, "percent of lines that are ERROR lines")
	flag.Parse()

	rate, size, rotate := mustSize(*rateS), mustSize(*sizeS), mustSize(*rotateS)
	f, w, cur := openLog(*out)

	services := []string{"api", "billing", "auth", "search", "orders"}
	paths := []string{"/api/v1/items", "/api/v1/orders", "/api/v1/users/me", "/api/v1/search", "/healthz"}
	codes := []string{"DB_TIMEOUT", "PAYMENT_DECLINED", "UPSTREAM_502", "CACHE_MISS_STORM", "AUTH_EXPIRED"}

	var written, sinceTick int64
	start, tick := time.Now(), time.Now()
	buf := make([]byte, 0, 512)
	for size == 0 || written < size {
		now := time.Now()
		buf = buf[:0]
		buf = now.UTC().AppendFormat(buf, "2006-01-02T15:04:05.000Z")
		svc := services[rand.IntN(len(services))]
		if rand.Float64()*100 < *errPct {
			code := codes[rand.IntN(len(codes))]
			buf = fmt.Appendf(buf, " ERROR service=%s code=%s req=%016x msg=\"request failed after %dms\" trace=%032x\n",
				svc, code, rand.Uint64(), rand.IntN(5000), rand.Uint64())
		} else {
			buf = fmt.Appendf(buf, " INFO service=%s req=%016x method=GET path=%s status=200 took=%dms bytes=%d ua=\"Mozilla/5.0 (X11; Linux x86_64) grepalert-loadgen\" trace=%032x\n",
				svc, rand.Uint64(), paths[rand.IntN(len(paths))], rand.IntN(300), rand.IntN(50000), rand.Uint64())
		}
		n, err := w.Write(buf)
		if err != nil {
			log.Fatal(err)
		}
		written += int64(n)
		cur += int64(n)
		sinceTick += int64(n)

		if rotate > 0 && cur >= rotate {
			w.Flush()
			f.Close()
			os.Remove(*out + ".1")
			if err := os.Rename(*out, *out+".1"); err != nil {
				log.Fatal(err)
			}
			f, w, cur = openLog(*out)
		}
		// Rate limiting in 10ms slices keeps the output smooth.
		if rate > 0 && sinceTick >= rate/100 {
			w.Flush()
			if d := 10*time.Millisecond - time.Since(tick); d > 0 {
				time.Sleep(d)
			}
			tick, sinceTick = time.Now(), 0
		}
	}
	w.Flush()
	f.Close()
	el := time.Since(start)
	fmt.Printf("wrote %.2f GB in %s (%.0f MB/s)\n", float64(written)/1e9, el.Round(time.Millisecond), float64(written)/1e6/el.Seconds())
}

func openLog(path string) (*os.File, *bufio.Writer, int64) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		log.Fatal(err)
	}
	fi, _ := f.Stat()
	return f, bufio.NewWriterSize(f, 1<<20), fi.Size()
}

func mustSize(s string) int64 {
	s = strings.ToUpper(strings.TrimSpace(s))
	mult := int64(1)
	for _, u := range []struct {
		suf string
		m   int64
	}{{"GB", 1e9}, {"MB", 1e6}, {"KB", 1e3}, {"G", 1e9}, {"M", 1e6}, {"K", 1e3}, {"B", 1}} {
		if strings.HasSuffix(s, u.suf) {
			s, mult = strings.TrimSuffix(s, u.suf), u.m
			break
		}
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		log.Fatalf("invalid size %q", s)
	}
	return int64(v * float64(mult))
}
