Summary:    Amazon CloudWatch Agent
Name:       amazon-cloudwatch-agent
Version:    %{AGENT_VERSION}
Release:    1
License:    MIT License. Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
Group:      Applications/CloudWatch-Agent

BuildRoot: %{_tmppath}/%{name}-%{version}-%{release}-buildroot-%(%{__id_u} -n)
Source:     amazon-cloudwatch-agent.tar.gz

%define _enable_debug_packages 0
%define debug_package %{nil}
%define _source_payload w6.gzdio
%define _binary_payload w6.gzdio

%prep
%setup -c %{name}-%{version}

%description
This package provides daemon of Amazon CloudWatch Agent

%install

rm -rf $RPM_BUILD_ROOT
mkdir $RPM_BUILD_ROOT
cp -r %{_topdir}/BUILD/%{name}-%{version}/*  $RPM_BUILD_ROOT/

############################# create the symbolic links
# bin
mkdir -p ${RPM_BUILD_ROOT}/usr/bin
ln -f -s /var/aws/amazon-cloudwatch-agent/bin/amazon-cloudwatch-agent-ctl ${RPM_BUILD_ROOT}/usr/bin/amazon-cloudwatch-agent-ctl
# etc
mkdir -p ${RPM_BUILD_ROOT}/etc/amazon
ln -f -s /var/aws/amazon-cloudwatch-agent/etc ${RPM_BUILD_ROOT}/etc/amazon/amazon-cloudwatch-agent
# log
mkdir -p ${RPM_BUILD_ROOT}/var/log/amazon
ln -f -s /var/aws/amazon-cloudwatch-agent/logs ${RPM_BUILD_ROOT}/var/log/amazon/amazon-cloudwatch-agent
# pid
mkdir -p ${RPM_BUILD_ROOT}/var/run/amazon
ln -f -s /var/aws/amazon-cloudwatch-agent/var ${RPM_BUILD_ROOT}/var/run/amazon/amazon-cloudwatch-agent

%files
%dir /var/aws/amazon-cloudwatch-agent
%dir /var/aws/amazon-cloudwatch-agent/bin
%dir /var/aws/amazon-cloudwatch-agent/doc
%dir /var/aws/amazon-cloudwatch-agent/etc
%dir /var/aws/amazon-cloudwatch-agent/etc/amazon-cloudwatch-agent.d
%dir /var/aws/amazon-cloudwatch-agent/logs
%dir /var/aws/amazon-cloudwatch-agent/var
/var/aws/amazon-cloudwatch-agent/bin/amazon-cloudwatch-agent
/var/aws/amazon-cloudwatch-agent/bin/amazon-cloudwatch-agent-ctl
/var/aws/amazon-cloudwatch-agent/bin/CWAGENT_VERSION
/var/aws/amazon-cloudwatch-agent/bin/config-translator
/var/aws/amazon-cloudwatch-agent/bin/config-downloader
/var/aws/amazon-cloudwatch-agent/bin/amazon-cloudwatch-agent-config-wizard
/var/aws/amazon-cloudwatch-agent/bin/start-amazon-cloudwatch-agent
/var/aws/amazon-cloudwatch-agent/bin/opentelemetry-jmx-metrics.jar
/var/aws/amazon-cloudwatch-agent/doc/amazon-cloudwatch-agent-schema.json
%config(noreplace) /var/aws/amazon-cloudwatch-agent/etc/common-config.toml
/var/aws/amazon-cloudwatch-agent/LICENSE
/var/aws/amazon-cloudwatch-agent/NOTICE

/var/aws/amazon-cloudwatch-agent/THIRD-PARTY-LICENSES
/var/aws/amazon-cloudwatch-agent/RELEASE_NOTES
/etc/init/amazon-cloudwatch-agent.conf
/etc/systemd/system/amazon-cloudwatch-agent.service

/usr/bin/amazon-cloudwatch-agent-ctl
/etc/amazon/amazon-cloudwatch-agent
/var/log/amazon/amazon-cloudwatch-agent
/var/run/amazon/amazon-cloudwatch-agent

%pre
# Stop the agent before upgrades.
if [ $1 -ge 2 ]; then
    if [ -x /var/aws/amazon-cloudwatch-agent/bin/amazon-cloudwatch-agent-ctl ]; then
        /var/aws/amazon-cloudwatch-agent/bin/amazon-cloudwatch-agent-ctl -a prep-restart
        /var/aws/amazon-cloudwatch-agent/bin/amazon-cloudwatch-agent-ctl -a stop
    fi
fi

if ! grep "^cwagent:" /etc/group >/dev/null 2>&1; then
    groupadd -r cwagent >/dev/null 2>&1
    echo "create group cwagent, result: $?"
fi

if ! id cwagent >/dev/null 2>&1; then
    useradd -r -M cwagent -d /home/cwagent -g cwagent -c "Cloudwatch Agent" -s $(test -x /sbin/nologin && echo /sbin/nologin || (test -x /usr/sbin/nologin && echo /usr/sbin/nologin || (test -x /bin/false && echo /bin/false || echo /bin/sh))) >/dev/null 2>&1
    echo "create user cwagent, result: $?"
fi

%preun
# Stop the agent after uninstall
if [ $1 -eq 0 ] ; then
    if [ -x /var/aws/amazon-cloudwatch-agent/bin/amazon-cloudwatch-agent-ctl ]; then
        /var/aws/amazon-cloudwatch-agent/bin/amazon-cloudwatch-agent-ctl -a preun
    fi
fi

%posttrans
# restart agent after upgrade
if [ -x /var/aws/amazon-cloudwatch-agent/bin/amazon-cloudwatch-agent-ctl ]; then
    /var/aws/amazon-cloudwatch-agent/bin/amazon-cloudwatch-agent-ctl -a cond-restart
fi

%clean