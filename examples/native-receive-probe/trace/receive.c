// macOS diagnostic interposer. Observes owned local test-room UDP reads only.
// No payload copying, packet edits, sleeps, admission, or receive policy.
#include <sys/socket.h>
#include <netinet/in.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <errno.h>
#include <pthread.h>
#include <time.h>
#include <unistd.h>
#include <string.h>

#define CAPACITY 65536
struct record { int64_t at; uint32_t ts, ssrc; uint16_t seq, port; int fd, bytes, api; };
static struct record records[CAPACITY];
static size_t count, dropped;
static pthread_mutex_t lock = PTHREAD_MUTEX_INITIALIZER;
static int enabled;
__attribute__((constructor)) static void setup(void) { enabled=getenv("PROBE_PACKET_TRACE")!=NULL; }
static int64_t mono(void) {struct timespec t;clock_gettime(CLOCK_MONOTONIC,&t);return (int64_t)t.tv_sec*1000000000+t.tv_nsec;}
static uint32_t be32(const unsigned char* p) {return ((uint32_t)p[0]<<24)|((uint32_t)p[1]<<16)|((uint32_t)p[2]<<8)|p[3];}
static void capture(int fd,const unsigned char* data,size_t size,const struct sockaddr* peer,int api,int64_t at) {
 if (!enabled || size<12 || (data[0]>>6)!=2 || (data[1]>=192 && data[1]<=223)) return;
 struct sockaddr_storage storage; socklen_t n=sizeof(storage);
 if (!peer) {if(getpeername(fd,(struct sockaddr*)&storage,&n))return;peer=(struct sockaddr*)&storage;}
 uint16_t port=0;
 if(peer->sa_family==AF_INET)port=ntohs(((const struct sockaddr_in*)peer)->sin_port);
 if(peer->sa_family==AF_INET6)port=ntohs(((const struct sockaddr_in6*)peer)->sin6_port);
 if(port<19000 || port>19100)return;
 pthread_mutex_lock(&lock);
 if(count<CAPACITY)records[count++]=(struct record){at,be32(data+4),be32(data+8),(uint16_t)((data[2]<<8)|data[3]),port,fd,(int)size,api};
 else dropped++;
 pthread_mutex_unlock(&lock);
}
int probe_packet_trace_flush(void) {
 const char* path=getenv("PROBE_PACKET_TRACE"); if(!path)return 0;
 FILE* f=fopen(path,"wx");if(!f)return -1;
 pthread_mutex_lock(&lock);
 fprintf(f,"{\"event\":\"trace\",\"pid\":%d,\"packets\":%zu,\"dropped\":%zu}\n",getpid(),count,dropped);
 for(size_t i=0;i<count;i++) {struct record r=records[i];
  fprintf(f,"{\"at\":%lld,\"seq\":%u,\"ts\":%u,\"ssrc\":%u,\"port\":%u,\"fd\":%d,\"bytes\":%d,\"api\":%d}\n",(long long)r.at,r.seq,r.ts,r.ssrc,r.port,r.fd,r.bytes,r.api);
 }
 int failed=ferror(f);pthread_mutex_unlock(&lock);
 return fclose(f) || failed ? -1 : 0;
}
static ssize_t traced_recvfrom(int fd,void* buf,size_t size,int flags,struct sockaddr* peer,socklen_t* n) {
 ssize_t r=recvfrom(fd,buf,size,flags,peer,n);int saved=errno;
 if(r>0 && (size_t)r<=size)capture(fd,buf,(size_t)r,peer,1,mono());
 errno=saved;return r;
}
static ssize_t traced_recv(int fd,void* buf,size_t size,int flags) {
 ssize_t r=recv(fd,buf,size,flags);int saved=errno;
 if(r>0 && (size_t)r<=size)capture(fd,buf,(size_t)r,NULL,2,mono());
 errno=saved;return r;
}
static ssize_t traced_recvmsg(int fd,struct msghdr* msg,int flags) {
 ssize_t r=recvmsg(fd,msg,flags);int saved=errno;
 if(r>=12) {
  int64_t at=mono();unsigned char head[12];size_t copied=0;
  for(int i=0;i<msg->msg_iovlen && copied<12;i++) {
   size_t n=msg->msg_iov[i].iov_len; if(n>12-copied)n=12-copied;
   memcpy(head+copied,msg->msg_iov[i].iov_base,n);copied+=n;
  }
  if(copied==12)capture(fd,head,(size_t)r,msg->msg_name,3,at);
 }
 errno=saved;return r;
}
#define INTERPOSE(replacement,original) \
 __attribute__((used)) static struct {const void* replacement;const void* original;} interpose_##original \
 __attribute__((section("__DATA,__interpose")))={(const void*)replacement,(const void*)original};
INTERPOSE(traced_recvfrom,recvfrom)
INTERPOSE(traced_recv,recv)
INTERPOSE(traced_recvmsg,recvmsg)

#ifdef TRACE_SELFTEST
#include <assert.h>
int main(void) {
 enabled=1;
 struct sockaddr_in p={.sin_family=AF_INET,.sin_port=htons(19001)};
 unsigned char rtp[12]={0x80,111,0xff,0xff,0,0,3,0xc0,0,0,0,42};
 capture(3,rtp,12,(struct sockaddr*)&p,1,99);
 assert(count==1 && records[0].seq==65535 && records[0].ts==960 && records[0].ssrc==42 && records[0].at==99);
 rtp[1]=200;capture(3,rtp,12,(struct sockaddr*)&p,1,100);assert(count==1);
 rtp[1]=111;p.sin_port=htons(9999);capture(3,rtp,12,(struct sockaddr*)&p,1,100);assert(count==1);
 p.sin_port=htons(19001);capture(3,rtp,11,(struct sockaddr*)&p,1,100);assert(count==1);
 errno=0;assert(traced_recv(-1,rtp,sizeof(rtp),0)==-1 && errno==EBADF);
 errno=0;socklen_t len=sizeof(p);assert(traced_recvfrom(-1,rtp,sizeof(rtp),0,(struct sockaddr*)&p,&len)==-1 && errno==EBADF);
 struct msghdr msg={0};errno=0;ssize_t expected=recvmsg(-1,&msg,0);int expected_errno=errno;
 errno=0;assert(traced_recvmsg(-1,&msg,0)==expected && errno==expected_errno);
 count=CAPACITY;capture(3,rtp,12,(struct sockaddr*)&p,1,100);assert(dropped==1);
 return 0;
}
#endif
