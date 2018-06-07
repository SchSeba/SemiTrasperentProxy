FROM fedora:27

ENV container docker

RUN curl --output /etc/yum.repos.d/fedora-virt-preview.repo https://fedorapeople.org/groups/virt/virt-preview/fedora-virt-preview.repo

RUN yum -y update nettle && \
  yum install -y \
  net-tools \
  libcap \
  libcap-devel \
  iptables \
  tcpdump \
  nmap-ncat \
  iptables2 \
  iproute   \
  ebtables  \
  make      \
  wget      \
  gcc       \ 
  pcre-static \
  pcre-devel  \
  haproxy

COPY tproxy_example /usr/bin/tproxy_example
COPY entrypoint.sh /entrypoint.sh

CMD ["/entrypoint.sh"]
