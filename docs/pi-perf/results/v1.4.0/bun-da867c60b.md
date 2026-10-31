# Bun 1.4.4-canary.1+000ef5037 → +da867c60b

Same Pi (getpie-dev/pi pitch-v1.4.0, d95a12ee1), all five patches, AOT on Apple M4, `bun bench/run.ts --rounds 3 --warmup 1` (machine under load ~12). No scenario more than 5% worse; `bench/e2e/run.ts` all passed on the new build. The 32 combos were not rebuilt.

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
