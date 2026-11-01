# Bun 1.4.4-canary.1+000ef5037 → +da867c60b

Same Pi (getpie-dev/pi pix-v1.4.0, d95a12ee1), all five patches, AOT on Apple M4, `bun bench/run.ts --rounds 3 --warmup 1` (machine under load ~12). No scenario more than 5% worse; `bench/e2e/run.ts` all passed on the new build. Combos are rebuilt only when this or `base` vs `all` shows a loss (pi-perf-upgrade skill).

```
scenario     build        metric           median                min–max  vs oldbun  n
startup      oldbun       wall_s            0.033            0.025–0.047             3
startup      newbun       wall_s            0.029            0.026–0.030     -13.8%  3
startup      oldbun       cpu_s             0.029            0.020–0.030             3
startup      newbun       cpu_s             0.021            0.021–0.027     -26.9%  3
startup      oldbun       peak_rss_mb      43.100          43.100–43.100             3
startup      newbun       peak_rss_mb      42.900          42.900–42.900      -0.5%  3
write:200    oldbun       wall_s            0.117            0.106–0.127             3
write:200    newbun       wall_s            0.108            0.107–0.113      -7.7%  3
write:200    oldbun       cpu_s             0.112            0.103–0.115             3
write:200    newbun       cpu_s             0.105            0.104–0.110      -5.6%  3
write:200    oldbun       peak_rss_mb      81.200          80.100–82.100             3
write:200    newbun       peak_rss_mb      81.500          80.700–82.300       0.4%  3
tuiwrite:200 oldbun       wall_s            0.311            0.306–0.315             3
tuiwrite:200 newbun       wall_s            0.309            0.307–0.310      -0.6%  3
tuiwrite:200 oldbun       cpu_s             0.190            0.190–0.190             3
tuiwrite:200 newbun       cpu_s             0.190            0.180–0.190       0.0%  3
tuiwrite:200 oldbun       rss_mb          105.600        104.800–105.800             3
tuiwrite:200 newbun       rss_mb          105.800        105.300–105.900       0.2%  3
md:20000     oldbun       wall_s           17.571          17.561–17.577             3
md:20000     newbun       wall_s           17.575          17.565–17.594       0.0%  3
md:20000     oldbun       cpu_s             1.490            1.380–1.500             3
md:20000     newbun       cpu_s             1.520            1.490–1.560       2.0%  3
md:20000     oldbun       rss_mb           92.200          91.700–92.600             3
md:20000     newbun       rss_mb           91.300          90.300–92.800      -1.0%  3
md:60000     oldbun       wall_s           53.027          52.971–53.184             3
md:60000     newbun       wall_s           52.880          52.814–52.979      -0.3%  3
md:60000     oldbun       cpu_s             4.810            3.740–4.840             3
md:60000     newbun       cpu_s             4.420            3.400–4.860      -8.1%  3
md:60000     oldbun       rss_mb          125.000        122.400–127.600             3
md:60000     newbun       rss_mb          123.500        106.500–126.200      -1.2%  3

```

## Patches on the new Bun: base vs all

Same Bun (+da867c60b), Pi v1.4.0 without patches (`base`) and with all five (`all`, f9731d41a), on Apple M1 Pro,
`bun bench/run.ts --rounds 3 --warmup 1`. Every gain holds; startup differs by about 10 ms, within its range.

```
scenario     build        metric           median                min–max    vs base  n
startup      base         wall_s            0.037            0.029–0.159             3
startup      all          wall_s            0.043            0.031–0.164      18.0%  3
startup      base         cpu_s             0.025            0.025–0.052             3
startup      all          cpu_s             0.037            0.025–0.053      46.2%  3
startup      base         peak_rss_mb      42.900          42.900–42.900             3
startup      all          peak_rss_mb      42.800          42.800–42.800      -0.2%  3
write:200    base         wall_s           14.023          13.962–14.531             3
write:200    all          wall_s            0.159            0.153–0.335     -98.9%  3
write:200    base         cpu_s            13.973          13.971–14.310             3
write:200    all          cpu_s             0.147            0.147–0.184     -98.9%  3
write:200    base         peak_rss_mb     263.800        257.200–288.300             3
write:200    all          peak_rss_mb      80.000          79.800–80.100     -69.7%  3
tuiwrite:200 base         wall_s           22.927          22.926–22.950             3
tuiwrite:200 all          wall_s            0.384            0.383–0.388     -98.3%  3
tuiwrite:200 base         cpu_s            22.840          22.820–22.850             3
tuiwrite:200 all          cpu_s             0.250            0.240–0.260     -98.9%  3
tuiwrite:200 base         rss_mb          266.900        261.900–293.100             3
tuiwrite:200 all          rss_mb           95.200          95.100–97.100     -64.3%  3
md:20000     base         wall_s           17.600          17.591–17.637             3
md:20000     all          wall_s           17.645          17.632–17.732       0.3%  3
md:20000     base         cpu_s             7.040            7.040–7.070             3
md:20000     all          cpu_s             1.680            1.640–1.690     -76.1%  3
md:20000     base         rss_mb          110.600        110.600–112.900             3
md:20000     all          rss_mb           82.300          82.200–82.600     -25.6%  3
md:60000     base         wall_s           52.474          52.437–52.575             3
md:60000     all          wall_s           53.529          53.514–53.619       2.0%  3
md:60000     base         cpu_s            32.170          32.040–32.250             3
md:60000     all          cpu_s             5.270            5.260–5.290     -83.6%  3
md:60000     base         rss_mb          153.400        149.300–159.400             3
md:60000     all          rss_mb          117.000        113.800–118.500     -23.7%  3
```
