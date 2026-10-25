/* Small Linux wait4 launcher: fork from a small process so the CLI's peak RSS
 * does not inherit the Python benchmark driver's resident memory floor.
 * Usage: benchmark-exec metrics.json executable [args...]
 */
#define _GNU_SOURCE
#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <sys/resource.h>
#include <sys/types.h>
#include <sys/wait.h>
#include <time.h>
#include <unistd.h>

static double milliseconds(struct timeval t) {
    return t.tv_sec * 1000.0 + t.tv_usec / 1000.0;
}

int main(int argc, char **argv) {
    if (argc < 3) return 125;
    struct timespec start, end;
    struct rusage usage;
    int status;
    clock_gettime(CLOCK_MONOTONIC, &start);
    pid_t child = fork();
    if (child < 0) { perror("fork"); return 125; }
    if (child == 0) {
        execv(argv[2], argv + 2);
        perror("execv");
        _exit(127);
    }
    while (wait4(child, &status, 0, &usage) < 0) {
        if (errno != EINTR) { perror("wait4"); return 125; }
    }
    clock_gettime(CLOCK_MONOTONIC, &end);
    double wall = (end.tv_sec - start.tv_sec) * 1000.0 +
                  (end.tv_nsec - start.tv_nsec) / 1000000.0;
    FILE *metrics = fopen(argv[1], "w");
    if (!metrics) { perror("fopen"); return 125; }
    int code = WIFEXITED(status) ? WEXITSTATUS(status) : 128 + WTERMSIG(status);
    fprintf(metrics, "{\"wall_ms\":%.6f,\"user_ms\":%.6f,"
            "\"system_ms\":%.6f,\"max_rss_kib\":%ld,\"exit_code\":%d}\n",
            wall, milliseconds(usage.ru_utime), milliseconds(usage.ru_stime),
            usage.ru_maxrss, code);
    if (fclose(metrics)) { perror("fclose"); return 125; }
    return code;
}
