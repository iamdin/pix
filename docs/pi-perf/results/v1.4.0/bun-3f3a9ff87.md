# Bun 1.4.4-canary.1+da867c60b → +3f3a9ff87

The downloads of +da867c60b were gone, and a release on GitHub Actions downloads the pinned Bun
(`scripts/fetch-bun.sh`). Same Pi (getpie-dev/pi pix-v1.4.0, f9731d41a), all five patches, AOT on Apple M4,
`bun bench/run.ts --rounds 3 --warmup 1` (`bun-3f3a9ff87.jsonl`). CPU and wall are the same or better. RSS is 3–12 MB
(5–10%) higher in every scenario, startup included, so it comes from Bun, not a patch; accepted, as the previous canary
can no longer be downloaded. `bench/e2e/run.ts` all passed on the new build.

```
scenario     build        metric           median                min–max     vs old  n
startup      old          wall_s            0.035            0.026–0.148             3
startup      new          wall_s            0.028            0.027–0.073     -19.5%  3
startup      old          cpu_s             0.030            0.022–0.040             3
startup      new          cpu_s             0.022            0.021–0.032     -27.0%  3
startup      old          peak_rss_mb      42.500          42.500–42.500             3
startup      new          peak_rss_mb      45.500          45.400–45.500       7.1%  3
write:200    old          wall_s            0.118            0.112–0.145             3
write:200    new          wall_s            0.109            0.107–0.121      -7.5%  3
write:200    old          cpu_s             0.108            0.105–0.109             3
write:200    new          cpu_s             0.103            0.101–0.104      -4.6%  3
write:200    old          peak_rss_mb      80.900          79.600–81.600             3
write:200    new          peak_rss_mb      85.400          84.200–86.000       5.6%  3
tuiwrite:200 old          wall_s            0.306            0.289–0.310             3
tuiwrite:200 new          wall_s            0.296            0.285–0.301      -3.3%  3
tuiwrite:200 old          cpu_s             0.190            0.170–0.190             3
tuiwrite:200 new          cpu_s             0.170            0.170–0.180     -10.5%  3
tuiwrite:200 old          rss_mb          105.000        104.300–105.100             3
tuiwrite:200 new          rss_mb          115.900        110.600–116.200      10.4%  3
md:20000     old          wall_s           17.565          17.276–17.610             3
md:20000     new          wall_s           17.592          17.275–17.595       0.2%  3
md:20000     old          cpu_s             1.430            1.130–1.460             3
md:20000     new          cpu_s             1.380            1.360–1.460      -3.5%  3
md:20000     old          rss_mb           90.700          90.700–91.600             3
md:20000     new          rss_mb           95.600          95.200–96.000       5.4%  3
md:60000     old          wall_s           52.907          52.002–52.999             3
md:60000     new          wall_s           52.789          52.082–52.946      -0.2%  3
md:60000     old          cpu_s             4.430            4.270–4.550             3
md:60000     new          cpu_s             4.250            4.080–4.460      -4.1%  3
md:60000     old          rss_mb          125.600        121.900–129.200             3
md:60000     new          rss_mb          137.300        126.700–138.100       9.3%  3
```

## Patches on the new Bun: base vs all

Pi v1.4.0 without patches (`base`) and with all five (`all`), same machine and runs (`bun-3f3a9ff87-base-all.jsonl`).
Every gain holds.

```
scenario     build        metric           median                min–max    vs base  n
startup      base         wall_s            0.029            0.028–0.160             3
startup      all          wall_s            0.029            0.024–0.176       1.8%  3
startup      base         cpu_s             0.024            0.023–0.049             3
startup      all          cpu_s             0.024            0.021–0.047       0.8%  3
startup      base         peak_rss_mb      45.300          45.300–45.300             3
startup      all          peak_rss_mb      45.500          45.500–45.500       0.4%  3
write:200    base         wall_s            9.694            9.272–9.797             3
write:200    all          wall_s            0.132            0.114–0.185     -98.6%  3
write:200    base         cpu_s             9.595            9.284–9.731             3
write:200    all          cpu_s             0.115            0.103–0.117     -98.8%  3
write:200    base         peak_rss_mb     261.500        242.000–272.100             3
write:200    all          peak_rss_mb      85.300          85.000–86.900     -67.4%  3
tuiwrite:200 base         wall_s           15.737          15.241–15.793             3
tuiwrite:200 all          wall_s            0.302            0.297–0.308     -98.1%  3
tuiwrite:200 base         cpu_s            15.540          15.160–15.650             3
tuiwrite:200 all          cpu_s             0.180            0.170–0.180     -98.8%  3
tuiwrite:200 base         rss_mb          320.300        317.400–331.600             3
tuiwrite:200 all          rss_mb          115.200        111.600–116.800     -64.0%  3
md:20000     base         wall_s           17.261          17.256–17.582             3
md:20000     all          wall_s           17.312          17.286–17.555       0.3%  3
md:20000     base         cpu_s             5.480            5.350–5.550             3
md:20000     all          cpu_s             1.330            1.310–1.380     -75.7%  3
md:20000     base         rss_mb          152.700        147.600–154.200             3
md:20000     all          rss_mb           96.500          94.900–96.600     -36.8%  3
md:60000     base         wall_s           51.287          51.219–52.407             3
md:60000     all          wall_s           52.141          51.951–52.995       1.7%  3
md:60000     base         cpu_s            22.940          22.840–23.250             3
md:60000     all          cpu_s             4.290            4.050–4.390     -81.3%  3
md:60000     base         rss_mb          171.200        140.100–173.400             3
md:60000     all          rss_mb          128.100        125.900–129.600     -25.2%  3
```

## native-image-resize on the new PNG codec

Between the two canaries Bun replaced libspng with its own PNG codec (oven-sh/bun#47070), which `Bun.Image` and so
`native-image-resize` use. On Apple M4: the patch's `bun test ./test/image-resize-bun.bun.ts` passes on both (6/6).
`resizeImageWithBun()` from the fork on photo-like 3000×2000 inputs (gradients plus noise; the PNG encoded with
node:zlib, the JPEG from it at quality 90), median of 5:

| input | old (+da867c60b) | new (+3f3a9ff87) | output |
|---|---|---|---|
| PNG 15 MB → 2000×1333 PNG | 285 ms | 177 ms | byte-identical |
| JPEG 2.7 MB → 2000×1333 JPEG | 337 ms | 235 ms | byte-identical |
| PNG decode, resize, encode alone | 349 ms | 113 ms | |
